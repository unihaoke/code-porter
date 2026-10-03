package gateway

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// AckCommand Agent 上报任务结果与流式片段。
type AckCommand struct {
	TaskID    string
	AgentID   agent.ID
	LockToken string
	Status    port.AckStatus
	Chunks    []port.ChunkPayload
	Error     string
	Result    string
}

// AckResult 上报处理结果。
type AckResult struct {
	// Status 任务当前状态。
	Status task.Status
	// Retry 是否已重新入队等待重试。
	Retry bool
	// DeadLetter 是否已进入死信。
	DeadLetter bool
	// AcceptedChunks 本次被接收的片段数。
	AcceptedChunks int
}

// AckTaskUseCase 任务 ACK 用例：接收片段、裁决成功/失败/重试/死信/背压释放。
type AckTaskUseCase struct {
	taskRepo  task.TaskRepository
	queueRepo task.TaskQueueRepository
	registry  *AgentRegistry
	broker    port.TaskEventBroker
	clock     port.Clock
	log       port.Logger
	policy    TaskPolicy
}

// NewAckTaskUseCase 构造用例。
func NewAckTaskUseCase(
	taskRepo task.TaskRepository,
	queueRepo task.TaskQueueRepository,
	registry *AgentRegistry,
	broker port.TaskEventBroker,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *AckTaskUseCase {
	return &AckTaskUseCase{
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		registry:  registry,
		broker:    broker,
		clock:     clock,
		log:       log.With(port.F("uc", "ack_task")),
		policy:    policy.withDefaults(),
	}
}

// Execute 执行 ACK。
func (u *AckTaskUseCase) Execute(ctx context.Context, cmd AckCommand) (*AckResult, error) {
	if cmd.TaskID == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "taskId is required")
	}

	tid := task.ID(cmd.TaskID)
	t, err := u.taskRepo.Find(ctx, tid)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeNotFound, "task not found: "+cmd.TaskID, err)
	}
	if cmd.AgentID != "" && t.AgentID() != cmd.AgentID {
		return nil, apperr.New(apperr.CodeForbidden, "task does not belong to this agent")
	}

	now := u.clock.Now()
	res := &AckResult{Status: t.Status()}

	// 本地背压释放：Agent 拉取到本地队列满，主动放弃，任务回到网关队列。
	if cmd.Status == port.AckReleased {
		if err := t.Release(now); err != nil {
			return nil, err
		}
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return nil, err
		}
		if err := u.queueRepo.Requeue(ctx, t.AgentID(), tid); err != nil {
			return nil, apperr.Wrap(apperr.CodeInternal, "requeue task failed", err)
		}
		u.log.Info("task released by agent backpressure",
			port.F("task_id", cmd.TaskID), port.F("agent", string(t.AgentID())))
		res.Status = t.Status()
		return res, nil
	}

	// 任务锁校验：防止旧 Agent 实例或超时任务污染结果。
	if cmd.LockToken != "" && t.LockToken() != "" && cmd.LockToken != t.LockToken() {
		return nil, apperr.New(apperr.CodeConflict, "stale task lock token, ack ignored")
	}
	if t.Status() != task.StatusRunning {
		// 已终态：幂等返回，避免 Agent 重试上报造成错误。
		u.log.Warn("ack ignored, task not running",
			port.F("task_id", cmd.TaskID), port.F("status", t.Status().String()))
		res.Status = t.Status()
		return res, nil
	}

	// 1) 追加流式片段并实时扇出给等待中的调用方。
	for _, c := range cmd.Chunks {
		if c.Content == "" {
			continue
		}
		if _, err := t.AppendChunk(c.Content, now); err != nil {
			break
		}
		res.AcceptedChunks++
		_ = u.publish(ctx, port.TaskEvent{
			TaskID:  cmd.TaskID,
			Type:    port.EventChunk,
			Content: c.Content,
			At:      now,
		})
	}

	// 2) 终态裁决。
	switch cmd.Status {
	case port.AckProgress:
		// 仅上报片段，任务继续运行。
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return nil, err
		}
		res.Status = t.Status()
		return res, nil

	case port.AckSuccess:
		if err := t.Complete(cmd.Result, now); err != nil {
			return nil, err
		}
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return nil, err
		}
		_ = u.queueRepo.Remove(ctx, t.AgentID(), tid)
		// Done 事件携带完整结果：非流式调用直接取用，流式调用按已发送进度补齐增量，
		// 避免「只上报了片段、结果被丢弃」或「增量重复」两种问题。
		_ = u.publish(ctx, port.TaskEvent{
			TaskID:  cmd.TaskID,
			Type:    port.EventDone,
			Content: t.Result(),
			At:      now,
		})

	default: // AckFailed
		outcome, err := t.Fail(orDefault(cmd.Error, "task failed on agent"), now)
		if err != nil {
			return nil, err
		}
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return nil, err
		}
		if outcome == task.FailRetry {
			// 还有重试额度：回到网关队列，调用方继续等待。
			if err := u.queueRepo.Requeue(ctx, t.AgentID(), tid); err != nil {
				return nil, apperr.Wrap(apperr.CodeInternal, "requeue task failed", err)
			}
			res.Retry = true
			u.log.Warn("task failed, requeued for retry",
				port.F("task_id", cmd.TaskID),
				port.F("attempt", t.Attempts()),
				port.F("max_retry", t.MaxRetry()))
		} else {
			_ = u.queueRepo.Remove(ctx, t.AgentID(), tid)
			_ = u.publish(ctx, port.TaskEvent{
				TaskID:  cmd.TaskID,
				Type:    port.EventError,
				Code:    string(apperr.CodeMCPFailure),
				Message: t.ErrorMessage(),
				At:      now,
			})
			res.DeadLetter = t.Status() == task.StatusDeadLetter
		}
	}

	res.Status = t.Status()
	u.log.Info("task acked",
		port.F("task_id", cmd.TaskID),
		port.F("status", t.Status().String()),
		port.F("chunks", res.AcceptedChunks))
	return res, nil
}

func (u *AckTaskUseCase) publish(ctx context.Context, ev port.TaskEvent) error {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	return u.broker.Publish(ctx, ev)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
