package memory

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// AgentRepository Agent 仓储的内存实现。
type AgentRepository struct {
	mu sync.RWMutex
	// byID 实例 ID → 聚合副本。
	byID map[agent.ID]*agent.Agent
}

// NewAgentRepository 构造仓储。
func NewAgentRepository() *AgentRepository {
	return &AgentRepository{byID: make(map[agent.ID]*agent.Agent)}
}

// Save 新建或更新 Agent。
func (r *AgentRepository) Save(_ context.Context, a *agent.Agent) error {
	if a == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[a.ID()] = a.Clone()
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

// FindByIdentity 按归属 + 实例 ID 查询；归属不匹配同样视为不存在。
func (r *AgentRepository) FindByIdentity(_ context.Context, ownerID user.ID, id agent.ID) (*agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byID[id]
	if !ok || a.OwnerID() != ownerID {
		return nil, agent.ErrAgentNotFound
	}
	return a.Clone(), nil
}

// ListByOwner 列出某用户的全部 Agent。
func (r *AgentRepository) ListByOwner(_ context.Context, ownerID user.ID) ([]*agent.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*agent.Agent, 0)
	for _, a := range r.byID {
		if a.OwnerID() == ownerID {
			out = append(out, a.Clone())
		}
	}
	return out, nil
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
	delete(r.byID, id)
	return nil
}
