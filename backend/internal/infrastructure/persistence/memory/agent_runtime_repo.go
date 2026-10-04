package memory

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// RuntimeAgentRepository 在「只存身份的持久仓储」（MySQL 实现）之上叠加一层
// 进程内运行态缓存。
//
// 背景：MySQL agents 表只持久化身份列（归属/名称/注册时间/最近出现时间），
// 读回时通过 agent.RewriteIdentity 得到的总是 offline + 空健康快照的新副本；
// 而在线状态、健康快照、队列/并发上限是易失运行时态，按既定设计留在网关进程
// 内存（见 migrations/0001_init.sql 中 agents 表注释、cmd/gateway/main.go 装配
// 注释）。若把 MySQL 仓储直接交给 AgentRegistry，每次健康上报都是
// 「读到 offline → 内存改成 online → Save 只落身份列」，控制台将永远显示离线。
//
// 本装饰器让 registry 的「取副本 → 修改 → 回写」在同一网关进程的多次请求之间
// 保持运行态一致；身份仍以底层持久仓储为准。网关重启后缓存为空，实例短暂显示
// 离线，直到下一次 pull / 健康上报 / WebSocket 连接将其重新标记为在线。
type RuntimeAgentRepository struct {
	identity agent.Repository

	mu      sync.RWMutex
	runtime map[agent.ID]*agent.Agent
}

// NewRuntimeAgentRepository 包装一个只持久化身份的 Agent 仓储。
func NewRuntimeAgentRepository(identity agent.Repository) *RuntimeAgentRepository {
	return &RuntimeAgentRepository{
		identity: identity,
		runtime:  make(map[agent.ID]*agent.Agent),
	}
}

// Save 先持久化身份，成功后更新内存运行态（存副本）。
func (r *RuntimeAgentRepository) Save(ctx context.Context, a *agent.Agent) error {
	if a == nil {
		return nil
	}
	if err := r.identity.Save(ctx, a); err != nil {
		return err
	}
	r.mu.Lock()
	r.runtime[a.ID()] = a.Clone()
	r.mu.Unlock()
	return nil
}

// Find 优先返回缓存的运行态副本；缓存未命中时从身份仓储加载并纳入缓存。
func (r *RuntimeAgentRepository) Find(ctx context.Context, id agent.ID) (*agent.Agent, error) {
	r.mu.RLock()
	cached, ok := r.runtime[id]
	r.mu.RUnlock()
	if ok {
		return cached.Clone(), nil
	}
	a, err := r.identity.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	r.put(a)
	return a.Clone(), nil
}

// FindByIdentity 鉴权路径：缓存命中且归属一致时直接返回，否则回落身份仓储。
func (r *RuntimeAgentRepository) FindByIdentity(ctx context.Context, ownerID user.ID, id agent.ID) (*agent.Agent, error) {
	r.mu.RLock()
	cached, ok := r.runtime[id]
	r.mu.RUnlock()
	if ok && cached.OwnerID() == ownerID {
		return cached.Clone(), nil
	}
	a, err := r.identity.FindByIdentity(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	r.put(a)
	return a.Clone(), nil
}

// ListByOwner 列出实例，身份清单中的每条用缓存运行态覆盖。
func (r *RuntimeAgentRepository) ListByOwner(ctx context.Context, ownerID user.ID) ([]*agent.Agent, error) {
	list, err := r.identity.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return r.overlay(list), nil
}

// List 列出全部实例，身份清单中的每条用缓存运行态覆盖。
func (r *RuntimeAgentRepository) List(ctx context.Context) ([]*agent.Agent, error) {
	list, err := r.identity.List(ctx)
	if err != nil {
		return nil, err
	}
	return r.overlay(list), nil
}

// Delete 同时删除持久身份与内存运行态。
func (r *RuntimeAgentRepository) Delete(ctx context.Context, id agent.ID) error {
	if err := r.identity.Delete(ctx, id); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.runtime, id)
	r.mu.Unlock()
	return nil
}

// put 把首次从身份仓储读到的副本纳入缓存（仅当尚无更新的运行态时）。
func (r *RuntimeAgentRepository) put(a *agent.Agent) {
	if a == nil {
		return
	}
	r.mu.Lock()
	if _, ok := r.runtime[a.ID()]; !ok {
		r.runtime[a.ID()] = a.Clone()
	}
	r.mu.Unlock()
}

// overlay 用缓存运行态覆盖身份仓储返回的逐条副本；未缓存的保持 offline 身份态。
func (r *RuntimeAgentRepository) overlay(identityList []*agent.Agent) []*agent.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*agent.Agent, 0, len(identityList))
	for _, a := range identityList {
		if cached, ok := r.runtime[a.ID()]; ok {
			out = append(out, cached.Clone())
			continue
		}
		out = append(out, a.Clone())
	}
	return out
}
