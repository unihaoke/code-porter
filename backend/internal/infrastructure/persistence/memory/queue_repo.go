package memory

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// TaskQueueRepository Agent 私有任务队列的内存实现。
//
// 内部复用领域层的 task.Queue 实体承载 FIFO 与容量规则，
// 所有对外方法均为原子操作，避免高并发下的丢失更新。
type TaskQueueRepository struct {
	mu     sync.Mutex
	queues map[agent.ID]*task.Queue
}

// NewTaskQueueRepository 构造仓储。
func NewTaskQueueRepository() *TaskQueueRepository {
	return &TaskQueueRepository{queues: make(map[agent.ID]*task.Queue)}
}

// Ensure 确保队列存在并设置容量。
func (r *TaskQueueRepository) Ensure(_ context.Context, agentID agent.ID, maxLen int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.queues[agentID]
	if !ok {
		r.queues[agentID] = task.NewQueue(agentID, maxLen)
		return nil
	}
	if maxLen > 0 && q.MaxLen() != maxLen {
		// 容量变更：保留已有元素重建队列。
		nq := task.NewQueue(agentID, maxLen)
		for _, id := range q.Peek(q.Len()) {
			_ = nq.Enqueue(id)
		}
		r.queues[agentID] = nq
	}
	return nil
}

// Enqueue 入队。
func (r *TaskQueueRepository) Enqueue(_ context.Context, agentID agent.ID, taskID task.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueOf(agentID).Enqueue(taskID)
}

// Requeue 重新入队（重试 / 背压释放）。
func (r *TaskQueueRepository) Requeue(_ context.Context, agentID agent.ID, taskID task.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueOf(agentID).Requeue(taskID)
	return nil
}

// Dequeue 出队最多 n 个。
func (r *TaskQueueRepository) Dequeue(_ context.Context, agentID agent.ID, n int) ([]task.ID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueOf(agentID).Dequeue(n), nil
}

// Remove 移除任务。
func (r *TaskQueueRepository) Remove(_ context.Context, agentID agent.ID, taskID task.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueOf(agentID).Remove(taskID)
	return nil
}

// Len 队列长度。
func (r *TaskQueueRepository) Len(_ context.Context, agentID agent.ID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueOf(agentID).Len(), nil
}

// MaxLen 队列容量。
func (r *TaskQueueRepository) MaxLen(_ context.Context, agentID agent.ID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queueOf(agentID).MaxLen(), nil
}

// Snapshot 返回各 Agent 队列长度快照（运维/监控用）。
func (r *TaskQueueRepository) Snapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.queues))
	for id, q := range r.queues {
		out[string(id)] = q.Len()
	}
	return out
}

func (r *TaskQueueRepository) queueOf(agentID agent.ID) *task.Queue {
	q, ok := r.queues[agentID]
	if !ok {
		q = task.NewQueue(agentID, task.DefaultQueueMaxLen)
		r.queues[agentID] = q
	}
	return q
}
