package task

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/agent"
)

// TaskRepository 任务仓储端口（出站接口，由基础设施层实现）。
//
// MVP 使用内存实现；V0.3 可替换为 Redis 实现而不影响领域层。
type TaskRepository interface {
	// Save 新建或更新任务。
	Save(ctx context.Context, t *Task) error
	// Find 按 ID 查询，不存在返回 ErrTaskNotFound。
	Find(ctx context.Context, id ID) (*Task, error)
	// FindByAgent 查询某个 Agent 的任务（statuses 为空表示全部）。
	FindByAgent(ctx context.Context, agentID agent.ID, statuses ...Status) ([]*Task, error)
	// FindByStatus 按状态批量查询，供生命周期守护扫描。
	FindByStatus(ctx context.Context, statuses ...Status) ([]*Task, error)
	// Delete 删除任务（仅用于死信清理/运维）。
	Delete(ctx context.Context, id ID) error
}

// TaskQueueRepository Agent 私有队列仓储端口。
//
// 接口刻意设计为「原子操作」而非「取出实体再回写」，
// 避免高并发下 read-modify-write 造成的丢失更新。
type TaskQueueRepository interface {
	// Ensure 确保队列存在并设定容量上限。
	Ensure(ctx context.Context, agentID agent.ID, maxLen int) error
	// Enqueue 队尾入队；队列满返回 apperr.CodeQueueFull。
	Enqueue(ctx context.Context, agentID agent.ID, taskID ID) error
	// Requeue 重新入队（重试 / 本地背压释放），不检查容量。
	Requeue(ctx context.Context, agentID agent.ID, taskID ID) error
	// Dequeue 出队最多 n 个任务 ID。
	Dequeue(ctx context.Context, agentID agent.ID, n int) ([]ID, error)
	// Remove 从队列中移除任务。
	Remove(ctx context.Context, agentID agent.ID, taskID ID) error
	// Len 当前队列长度。
	Len(ctx context.Context, agentID agent.ID) (int, error)
	// MaxLen 队列容量上限。
	MaxLen(ctx context.Context, agentID agent.ID) (int, error)
}
