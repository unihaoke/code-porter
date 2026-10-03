package memory

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/agent"
)

// AgentRepository Agent 仓储的内存实现。
type AgentRepository struct {
	mu      sync.RWMutex
	byID    map[agent.ID]*agent.Agent
	byToken map[string]agent.ID
}

// NewAgentRepository 构造仓储。
func NewAgentRepository() *AgentRepository {
	return &AgentRepository{
		byID:    make(map[agent.ID]*agent.Agent),
		byToken: make(map[string]agent.ID),
	}
}

// Save 新建或更新 Agent。
func (r *AgentRepository) Save(_ context.Context, a *agent.Agent) error {
	if a == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[a.ID()] = a.Clone()
	r.byToken[a.Token()] = a.ID()
	return nil
}

// Find 按 ID 查询。
func (r *AgentRepository) Find(_ context.Context, id agent.ID) (*agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byID[id]
	if !ok {
		return nil, agent.ErrAgentNotFound
	}
	return a.Clone(), nil
}

// FindByToken 按 Token 查询。
func (r *AgentRepository) FindByToken(_ context.Context, token string) (*agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byToken[token]
	if !ok {
		return nil, agent.ErrAgentNotFound
	}
	a, ok := r.byID[id]
	if !ok {
		return nil, agent.ErrAgentNotFound
	}
	return a.Clone(), nil
}

// List 列出全部 Agent。
func (r *AgentRepository) List(_ context.Context) ([]*agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*agent.Agent, 0, len(r.byID))
	for _, a := range r.byID {
		out = append(out, a.Clone())
	}
	return out, nil
}

// Delete 删除 Agent。
func (r *AgentRepository) Delete(_ context.Context, id agent.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.byID[id]; ok {
		delete(r.byToken, a.Token())
	}
	delete(r.byID, id)
	return nil
}
