package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// DefaultPullBatch 单次 Pull 默认最大任务数。
const DefaultPullBatch = 2

// MaxPullBatch 单次 Pull 硬上限，防止一次拉爆本地协程池。
const MaxPullBatch = 64

// PullTasksQuery 拉取任务查询。
type PullTasksQuery struct {
	// AgentID 拉取方。
	AgentID agent.ID
	// MaxBatch 期望拉取数量（由本地协程池剩余容量决定）。
	MaxBatch int
}

// PullTasksResult 拉取结果。
type PullTasksResult struct {
	// Tasks 已加锁的任务列表。
	Tasks []port.TaskDispatch
	// QueueLen 拉取后队列中剩余任务数。
	QueueLen int
}

// PullTasksUseCase Agent 主动拉取任务用例（Pull 队列模式核心）。
type PullTasksUseCase struct {
	taskRepo  task.TaskRepository
	queueRepo task.TaskQueueRepository
	registry  *AgentRegistry
	clock     port.Clock
	log       port.Logger
	policy    TaskPolicy
}

// NewPullTasksUseCase 构造用例。
func NewPullTasksUseCase(
	taskRepo task.TaskRepository,
	queueRepo task.TaskQueueRepository,
	registry *AgentRegistry,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *PullTasksUseCase {
	return &PullTasksUseCase{
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		registry:  registry,
		clock:     clock,
		log:       log.With(port.F("uc", "pull_tasks")),
		policy:    policy.withDefaults(),
	}
}

// Execute 执行拉取：出队 → 标记 running → 加任务锁 → 返回可执行的任务。
func (u *PullTasksUseCase) Execute(ctx context.Context, q PullTasksQuery) (*PullTasksResult, error) {
	if q.AgentID == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "agentId is required")
	}
	ag, err := u.registry.Resolve(ctx, q.AgentID)
	if err != nil {
		return nil, err
	}
	// 拉取本身即心跳：刷新在线状态。
	ag.MarkOnline(u.clock.Now())
	_ = u.registry.repo.Save(ctx, ag)

	batch := q.MaxBatch
	if batch <= 0 {
		batch = DefaultPullBatch
	}
	if batch > MaxPullBatch {
		batch = MaxPullBatch
	}

	ids, err := u.queueRepo.Dequeue(ctx, q.AgentID, batch)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "dequeue tasks failed", err)
	}

	now := u.clock.Now()
	out := make([]port.TaskDispatch, 0, len(ids))
	for _, tid := range ids {
		t, err := u.taskRepo.Find(ctx, tid)
		if err != nil {
			// 任务已被清理，跳过。
			continue
		}
		if t.Status() != task.StatusPending {
			// 终态或已在执行，跳过（可能是重试重复入队）。
			continue
		}
		lockToken := id.New("lock_")
		if err := t.MarkRunning(lockToken, u.policy.LockTimeout, now); err != nil {
			u.log.Warn("mark running failed", port.F("task_id", string(tid)), port.F("err", err.Error()))
			continue
		}
		if err := u.taskRepo.Save(ctx, t); err != nil {
			u.log.Error("save task failed", port.F("task_id", string(tid)), port.F("err", err.Error()))
			continue
		}
		out = append(out, toDispatch(t, u.policy.MCPTimeout))
	}

	queueLen, _ := u.queueRepo.Len(ctx, q.AgentID)
	if len(out) > 0 {
		u.log.Debug("tasks pulled",
			port.F("agent", string(q.AgentID)),
			port.F("count", len(out)),
			port.F("queue_len", queueLen))
	}
	return &PullTasksResult{Tasks: out, QueueLen: queueLen}, nil
}
