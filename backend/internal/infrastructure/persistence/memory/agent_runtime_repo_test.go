package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// identityOnlyAgentRepo 模拟 MySQL agents 仓储：Save 只落身份列，
// Find 读回时通过 RewriteIdentity 得到 offline + 空健康快照的新副本。
type identityOnlyAgentRepo struct {
	mu   sync.Mutex
	byID map[agent.ID]*agent.Agent
}

func newIdentityOnlyAgentRepo() *identityOnlyAgentRepo {
	return &identityOnlyAgentRepo{byID: map[agent.ID]*agent.Agent{}}
}

func (r *identityOnlyAgentRepo) reload(a *agent.Agent) *agent.Agent {
	cp := &agent.Agent{}
	cp.RewriteIdentity(a.ID(), a.OwnerID(), a.Name(), a.RegisteredAt(), a.LastHeartbeatAt())
	return cp
}

func (r *identityOnlyAgentRepo) Save(_ context.Context, a *agent.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[a.ID()] = r.reload(a)
	return nil
}

func (r *identityOnlyAgentRepo) Find(_ context.Context, id agent.ID) (*agent.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return nil, agent.ErrAgentNotFound
	}
	return r.reload(a), nil
}

func (r *identityOnlyAgentRepo) FindByIdentity(_ context.Context, ownerID user.ID, id agent.ID) (*agent.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok || a.OwnerID() != ownerID {
		return nil, agent.ErrAgentNotFound
	}
	return r.reload(a), nil
}

func (r *identityOnlyAgentRepo) ListByOwner(_ context.Context, ownerID user.ID) ([]*agent.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*agent.Agent, 0)
	for _, a := range r.byID {
		if a.OwnerID() == ownerID {
			out = append(out, r.reload(a))
		}
	}
	return out, nil
}

func (r *identityOnlyAgentRepo) List(_ context.Context) ([]*agent.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*agent.Agent, 0, len(r.byID))
	for _, a := range r.byID {
		out = append(out, r.reload(a))
	}
	return out, nil
}

func (r *identityOnlyAgentRepo) Delete(_ context.Context, id agent.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

// TestRuntimeAgentRepositoryKeepsRuntimeState 复现并验证修复：
// 底层身份仓储每次读回都是 offline 副本，运行态装饰器必须让
// 「取副本 → 改在线/健康 → 回写」在多次请求间保持在线状态与健康快照。
func TestRuntimeAgentRepositoryKeepsRuntimeState(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	identity := newIdentityOnlyAgentRepo()
	repo := NewRuntimeAgentRepository(identity)

	a, err := agent.Register(agent.Spec{ID: "agt_rt1", OwnerID: user.SeedAdminID, Name: "pc-rt", Now: now})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 底层身份仓储读回的必然是 offline（模拟 MySQL 真实行为）。
	raw, err := identity.Find(ctx, "agt_rt1")
	if err != nil {
		t.Fatalf("identity find: %v", err)
	}
	if raw.Status() != agent.StatusOffline {
		t.Fatalf("identity repo must restore offline, got %s", raw.Status())
	}

	// 模拟健康上报：解析（缓存未命中→offline 副本）→ 更新 → 回写。
	got, err := repo.FindByIdentity(ctx, user.SeedAdminID, "agt_rt1")
	if err != nil {
		t.Fatalf("find identity: %v", err)
	}
	got.UpdateHealth(agent.Health{
		CPUPercent: 10, MemPercent: 20, MaxConcurrency: 2,
		MCPs: []agent.MCPHealth{{Model: model.Trae, Available: true}},
	}, now.Add(time.Second))
	got.SetLimits(512, 2)
	if err := repo.Save(ctx, got); err != nil {
		t.Fatalf("save online: %v", err)
	}

	// 再次读：在线状态、健康快照、并发上限必须保留（修复前恒为 offline）。
	again, err := repo.Find(ctx, "agt_rt1")
	if err != nil {
		t.Fatalf("find again: %v", err)
	}
	if again.Status() != agent.StatusOnline {
		t.Fatalf("runtime status lost, want online got %s", again.Status())
	}
	if again.MaxConcurrency() != 2 {
		t.Fatalf("max concurrency lost: %d", again.MaxConcurrency())
	}
	if !again.Health().MCPAvailable(model.Trae) {
		t.Fatalf("mcp health snapshot lost: %+v", again.Health().MCPs)
	}

	// 跨属主查缓存实例同样不可见。
	if _, err := repo.FindByIdentity(ctx, user.ID("usr_bob"), "agt_rt1"); err != agent.ErrAgentNotFound {
		t.Fatalf("foreign owner must not see cached instance, got %v", err)
	}

	// List 用运行态覆盖身份行。
	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 || list[0].Status() != agent.StatusOnline {
		t.Fatalf("list overlay mismatch: %v %+v", err, list)
	}

	// 出参仍是副本：外部改动不污染缓存。
	list[0].MarkOffline()
	probe, _ := repo.Find(ctx, "agt_rt1")
	if probe.Status() != agent.StatusOnline {
		t.Fatalf("copy mutation leaked into runtime cache: %s", probe.Status())
	}

	// 删除后身份与运行态都不可见。
	if err := repo.Delete(ctx, "agt_rt1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Find(ctx, "agt_rt1"); err != agent.ErrAgentNotFound {
		t.Fatalf("agent must be gone after delete, got %v", err)
	}
}
