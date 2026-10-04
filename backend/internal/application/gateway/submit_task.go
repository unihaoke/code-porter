package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// SubmitTaskCommand 外部调用方提交任务的命令。
type SubmitTaskCommand struct {
	// OwnerID 租户（秘钥属主 / 登录用户）。
	OwnerID user.ID
	// APIKeyID 调用方 Key 标识（限流与审计）。
	APIKeyID string
	// AgentID 目标 Agent，空表示使用默认 Agent。
	AgentID agent.ID
	// Model 目标本地 AI 工具。
	Model model.Model
	// Prompt 直接给出的提示词（可选，为空时用 Messages 拼接）。
	Prompt string
	// Messages 多轮对话。
	Messages []task.Message
	// Files 代码上下文。
	Files []task.CodeFile
	// Operation 操作类型。
	Operation task.Operation
	// WorkDir 本地工作目录。
	WorkDir string
	// Stream 是否流式返回。
	Stream bool
	// Mode 期望的投递通路。
	Mode task.DeliveryMode
	// Temperature 采样温度。
	Temperature *float64
	// MaxTokens 最大输出长度。
	MaxTokens int
	// Permission 本地文件操作权限上限（秘钥上限与请求头收紧后的交集）；缺省 all。
	Permission task.Permission
}

// SubmitTaskResult 提交结果。
type SubmitTaskResult struct {
	// TaskID 任务 ID，可用于日志追踪与运维。
	TaskID string
	// Mode 实际生效的投递通路。
	Mode task.DeliveryMode
	// Stream 是否流式。
	Stream bool
	// Model 目标模型。
	Model model.Model
	// CreatedAt 创建时间。
	CreatedAt int64
	// Events 任务事件流：流式接口按片段推送，非流式接口等待终止事件。
	// 调用方在读完终止事件后应调用 Close 释放订阅。
	Events <-chan port.TaskEvent
	// Close 释放事件订阅（必须调用，避免订阅泄漏）。
	Close func()
}

// SubmitTaskUseCase 提交任务用例：校验 → 路由 → 入队/直连推送 → 订阅事件流。
type SubmitTaskUseCase struct {
	taskRepo  task.TaskRepository
	queueRepo task.TaskQueueRepository
	registry  *AgentRegistry
	broker    port.TaskEventBroker
	pusher    port.DirectPusher
	router    task.Router
	clock     port.Clock
	log       port.Logger
	policy    TaskPolicy
}

// NewSubmitTaskUseCase 构造用例。
func NewSubmitTaskUseCase(
	taskRepo task.TaskRepository,
	queueRepo task.TaskQueueRepository,
	registry *AgentRegistry,
	broker port.TaskEventBroker,
	pusher port.DirectPusher,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *SubmitTaskUseCase {
	return &SubmitTaskUseCase{
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		registry:  registry,
		broker:    broker,
		pusher:    pusher,
		router:    task.NewRouter(),
		clock:     clock,
		log:       log.With(port.F("uc", "submit_task")),
		policy:    policy.withDefaults(),
	}
}

// Execute 执行提交。
func (u *SubmitTaskUseCase) Execute(ctx context.Context, cmd SubmitTaskCommand) (*SubmitTaskResult, error) {
	if cmd.Model == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "model is required")
	}

	// TODO(T10): OwnerID 由鉴权中间件强制注入；过渡期 HTTP 层尚未全部接入，缺省挂种子 admin。
	ownerID := cmd.OwnerID
	if ownerID == "" {
		ownerID = user.SeedAdminID
	}

	ag, err := u.registry.ResolveForOwner(ctx, ownerID, cmd.AgentID)
	if err != nil {
		return nil, err
	}

	wsConnected := u.pusher != nil && u.pusher.Connected(string(ag.ID()))
	decision, err := u.router.Route(cmd.Mode, wsConnected)
	if err != nil {
		return nil, err
	}

	now := u.clock.Now()
	req := task.Request{
		Prompt:      cmd.Prompt,
		Messages:    cmd.Messages,
		Files:       cmd.Files,
		Operation:   cmd.Operation,
		WorkDir:     cmd.WorkDir,
		Temperature: cmd.Temperature,
		MaxTokens:   cmd.MaxTokens,
		Permission:  cmd.Permission,
	}
	req.Normalize() // 权限缺省 all，确保下发给 Agent 的值始终显式合法。
	t, err := task.NewTask(task.Spec{
		AgentID:     ag.ID(),
		OwnerID:     ownerID,
		Model:       cmd.Model,
		Mode:        decision.Mode,
		Stream:      cmd.Stream,
		MaxRetry:    u.policy.MaxRetry,
		LockTimeout: u.policy.LockTimeout,
		TTL:         u.policy.TTL,
		APIKeyID:    cmd.APIKeyID,
		Now:         now,
		Request:     req,
	})
	if err != nil {
		return nil, err
	}

	// 先订阅再入队，避免任务极快完成时丢事件。
	sub, err := u.broker.Subscribe(ctx, string(t.ID()), 256)
	if err != nil {
		return nil, err
	}

	// 入队 / 直连推送失败时统一回收订阅。
	fail := func(code apperr.Code, msg string, cause error) (*SubmitTaskResult, error) {
		u.broker.Unsubscribe(string(t.ID()))
		sub.Close()
		if cause != nil {
			return nil, apperr.Wrap(code, msg, cause)
		}
		return nil, apperr.New(code, msg)
	}

	switch decision.Mode {
	case task.ModeDirect:
		// 直连模式：不进队列，直接推送；失败即返回，调用方无需等待。
		lockToken := id.New("lock_")
		if err := t.MarkRunning(lockToken, u.policy.LockTimeout, now); err != nil {
			return fail(apperr.CodeInternal, "mark direct task running failed", err)
		}
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return fail(apperr.CodeInternal, "save task failed", err)
		}
		if err := u.pusher.Push(ctx, string(ag.ID()), toDispatch(t, u.policy.MCPTimeout)); err != nil {
			// 推送失败：把任务置为失败并通知等待中的调用方。
			_, _ = t.Fail("push to agent failed: "+apperr.MessageOf(err), u.clock.Now())
			_ = u.taskRepo.Save(ctx, t)
			return fail(apperr.CodeOf(err), "push task to agent failed", err)
		}
	default:
		if err := u.taskRepo.Save(ctx, t); err != nil {
			return fail(apperr.CodeInternal, "save task failed", err)
		}
		if err := u.queueRepo.Ensure(ctx, ag.ID(), u.policy.QueueMaxLen); err != nil {
			return fail(apperr.CodeInternal, "ensure agent queue failed", err)
		}
		if err := u.queueRepo.Enqueue(ctx, ag.ID(), t.ID()); err != nil {
			// 队列满 → 触发上游限流（HTTP 429）。
			_ = u.taskRepo.Delete(ctx, t.ID())
			return fail(apperr.CodeOf(err), apperr.MessageOf(err), err)
		}
	}

	u.log.Info("task submitted",
		port.F("task_id", string(t.ID())),
		port.F("agent", string(ag.ID())),
		port.F("model", cmd.Model.String()),
		port.F("mode", decision.Mode.String()),
		port.F("permission", req.Permission.String()),
		port.F("stream", cmd.Stream),
		port.F("reason", decision.Reason),
	)

	return &SubmitTaskResult{
		TaskID:    string(t.ID()),
		Mode:      decision.Mode,
		Stream:    cmd.Stream,
		Model:     cmd.Model,
		CreatedAt: now.Unix(),
		Events:    sub.Events(),
		Close:     sub.Close,
	}, nil
}
