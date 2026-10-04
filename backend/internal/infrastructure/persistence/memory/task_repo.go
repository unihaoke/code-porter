// Package memory 提供 MVP 阶段的内存仓储实现（PRD：MVP 使用内存队列，Redis 为可选扩展）。
//
// 所有仓储均通过「取副本 → 修改 → 回写」的方式工作，避免聚合内部状态被并发污染。
package memory

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// TaskRepository 任务仓储的内存实现。
type TaskRepository struct {
	mu sync.RWMutex
	m  map[task.ID]*task.Task
}

// NewTaskRepository 构造仓储。
func NewTaskRepository() *TaskRepository {
	return &TaskRepository{m: make(map[task.ID]*task.Task)}
}

// Save 新建或更新任务。
func (r *TaskRepository) Save(_ context.Context, t *task.Task) error {
	if t == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[t.ID()] = t.Clone()
	return nil
}

// Find 按 ID 查询，返回副本。
func (r *TaskRepository) Find(_ context.Context, id task.ID) (*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.m[id]
	if !ok {
		return nil, task.ErrTaskNotFound
	}
	return t.Clone(), nil
}

// FindByAgent 查询某 Agent 的任务。
func (r *TaskRepository) FindByAgent(_ context.Context, agentID agent.ID, statuses ...task.Status) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := toStatusSet(statuses)
	out := make([]*task.Task, 0)
	for _, t := range r.m {
		if t.AgentID() != agentID {
			continue
		}
		if !matchStatus(t.Status(), filter) {
			continue
		}
		out = append(out, t.Clone())
	}
	return out, nil
}

// FindByStatus 按状态批量查询。
func (r *TaskRepository) FindByStatus(_ context.Context, statuses ...task.Status) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := toStatusSet(statuses)
	out := make([]*task.Task, 0)
	for _, t := range r.m {
		if !matchStatus(t.Status(), filter) {
			continue
		}
		out = append(out, t.Clone())
	}
	return out, nil
}

// FindByOwner 查询某用户名下的任务。
func (r *TaskRepository) FindByOwner(_ context.Context, ownerID user.ID, statuses ...task.Status) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := toStatusSet(statuses)
	out := make([]*task.Task, 0)
	for _, t := range r.m {
		if t.OwnerID() != ownerID {
			continue
		}
		if !matchStatus(t.Status(), filter) {
			continue
		}
		out = append(out, t.Clone())
	}
	return out, nil
}

// Delete 删除任务。
func (r *TaskRepository) Delete(_ context.Context, id task.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
	return nil
}

// Count 返回当前任务总数（可观测性用）。
func (r *TaskRepository) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.m)
}

func toStatusSet(statuses []task.Status) map[task.Status]struct{} {
	if len(statuses) == 0 {
		return nil
	}
	s := make(map[task.Status]struct{}, len(statuses))
	for _, st := range statuses {
		s[st] = struct{}{}
	}
	return s
}

func matchStatus(st task.Status, set map[task.Status]struct{}) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[st]
	return ok
}
