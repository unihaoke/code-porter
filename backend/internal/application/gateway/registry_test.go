package gateway

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	"github.com/codeporter/code-porter/pkg/apperr"
)

func newTestRegistry(t *testing.T) (*AgentRegistry, *memory.AgentRepository) {
	t.Helper()
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	log := logging.New(io.Discard, logging.LevelError)
	r := NewAgentRegistry(agentRepo, queueRepo, port.RealClock{}, log, TaskPolicy{QueueMaxLen: 8})
	return r, agentRepo
}

func TestAuthenticateAndRegister(t *testing.T) {
	ctx := context.Background()
	r, repo := newTestRegistry(t)

	a, err := r.AuthenticateAndRegister(ctx, user.ID("usr_a"), agent.ID("agt_pc1"), "pc-1")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if a.OwnerID() != "usr_a" || a.Name() != "pc-1" {
		t.Fatalf("identity mismatch: %+v", a)
	}
	got, err := repo.FindByIdentity(ctx, "usr_a", "agt_pc1")
	if err != nil {
		t.Fatalf("persisted: %v", err)
	}
	_ = got

	// 再次接入：同一属主 → 刷新名字，不报错不重复建。
	a2, err := r.AuthenticateAndRegister(ctx, user.ID("usr_a"), agent.ID("agt_pc1"), "pc-renamed")
	if err != nil {
		t.Fatalf("re-register same owner: %v", err)
	}
	if a2.Name() != "pc-renamed" {
		t.Fatalf("name should refresh, got %s", a2.Name())
	}

	// 同实例 ID 换属主秘钥 → 403。
	if _, err := r.AuthenticateAndRegister(ctx, user.ID("usr_b"), agent.ID("agt_pc1"), "pc-b"); !errors.Is(err, agent.ErrOwnerMismatch) {
		t.Fatalf("foreign owner collision must be ErrOwnerMismatch, got %v", err)
	}

	// 缺参。
	if _, err := r.AuthenticateAndRegister(ctx, "", agent.ID("agt_x"), "x"); err == nil {
		t.Fatal("empty owner rejected")
	}
	if _, err := r.AuthenticateAndRegister(ctx, user.ID("usr_a"), agent.ID(""), "x"); err == nil {
		t.Fatal("empty instance id rejected")
	}
}

func TestResolveForOwnerMatrix(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	ownerA := user.ID("usr_a")
	ownerB := user.ID("usr_b")

	// 0 台 → 503。
	_, err := r.ResolveForOwner(ctx, ownerA, "")
	if apperr.CodeOf(err) != apperr.CodeUnavailable {
		t.Fatalf("zero agents -> unavailable, got %v", err)
	}

	if _, err := r.AuthenticateAndRegister(ctx, ownerA, agent.ID("agt_a1"), "a1"); err != nil {
		t.Fatal(err)
	}
	// 1 台 → 自动路由。
	got, err := r.ResolveForOwner(ctx, ownerA, "")
	if err != nil || got.ID() != "agt_a1" {
		t.Fatalf("single agent auto-route: %v %v", got, err)
	}

	if _, err := r.AuthenticateAndRegister(ctx, ownerA, agent.ID("agt_a2"), "a2"); err != nil {
		t.Fatal(err)
	}
	// 2 台未指定 → 409 + 清单。
	_, err = r.ResolveForOwner(ctx, ownerA, "")
	var mc *AgentsMultiChoiceError
	if !errors.As(err, &mc) {
		t.Fatalf("multiple agents -> 409 AgentsMultiChoiceError, got %T %v", err, err)
	}
	if len(mc.Choices) != 2 {
		t.Fatalf("choices should contain 2 agents, got %d", len(mc.Choices))
	}

	// 显式指定 → 精确路由。
	got, err = r.ResolveForOwner(ctx, ownerA, "agt_a2")
	if err != nil || got.ID() != "agt_a2" {
		t.Fatalf("explicit route: %v %v", got, err)
	}

	// 跨属主：B 看不到 A 的实例。
	if _, err := r.ResolveForOwner(ctx, ownerB, "agt_a1"); apperr.CodeOf(err) != apperr.CodeNotFound {
		t.Fatalf("foreign explicit -> not_found, got %v", err)
	}
	// B 自己名下仍为 0 → 503（A 的两台不算数）。
	if _, err := r.ResolveForOwner(ctx, ownerB, ""); apperr.CodeOf(err) != apperr.CodeUnavailable {
		t.Fatalf("owner B zero -> unavailable, got %v", err)
	}

	// 给 B 注册一台，A 未指定仍是 409、B 自动路由。
	if _, err := r.AuthenticateAndRegister(ctx, ownerB, agent.ID("agt_b1"), "b1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveForOwner(ctx, ownerA, ""); !errors.Is(err, apperr.ErrQueueFull) && apperr.CodeOf(err) != apperr.CodeConflict {
		t.Fatalf("A should still see 2 choices, got %v", err)
	}
	got, err = r.ResolveForOwner(ctx, ownerB, "")
	if err != nil || got.ID() != "agt_b1" {
		t.Fatalf("B auto-route: %v %v", got, err)
	}
}

func TestEvictOwner(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	ownerA := user.ID("usr_a")
	if _, err := r.AuthenticateAndRegister(ctx, ownerA, "agt_a1", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateAndRegister(ctx, ownerA, "agt_a2", "a2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateAndRegister(ctx, user.ID("usr_b"), "agt_b1", "b1"); err != nil {
		t.Fatal(err)
	}
	ids, err := r.EvictOwner(ctx, ownerA)
	if err != nil || len(ids) != 2 {
		t.Fatalf("evict: %v %v", ids, err)
	}
	if list, _ := r.ListForOwner(ctx, ownerA); len(list) != 0 {
		t.Fatalf("owner A should have no agents")
	}
	if list, _ := r.List(ctx); len(list) != 1 || list[0].ID() != "agt_b1" {
		t.Fatalf("owner B agents untouched")
	}
	// 幂等。
	if ids, err := r.EvictOwner(ctx, ownerA); err != nil || len(ids) != 0 {
		t.Fatalf("evict idempotent: %v %v", ids, err)
	}
}

// TestSubmitEnforcesTenancy 端到端验证任务只能路由到属主自己的实例。
func TestSubmitEnforcesTenancy(t *testing.T) {
	ctx := context.Background()
	log := logging.New(io.Discard, logging.LevelError)
	clock := port.RealClock{}
	taskRepo := memory.NewTaskRepository()
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	broker := broker.NewMemoryBroker()
	registry := NewAgentRegistry(agentRepo, queueRepo, clock, log, TaskPolicy{QueueMaxLen: 8})
	submit := NewSubmitTaskUseCase(taskRepo, queueRepo, registry, broker, nil, clock, log, TaskPolicy{QueueMaxLen: 8})

	if _, err := registry.AuthenticateAndRegister(ctx, user.ID("usr_a"), "agt_a1", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AuthenticateAndRegister(ctx, user.ID("usr_b"), "agt_b1", "b1"); err != nil {
		t.Fatal(err)
	}

	// A 指定 B 的实例 → not found，任务不得入队。
	_, err := submit.Execute(ctx, SubmitTaskCommand{
		OwnerID: user.ID("usr_a"),
		AgentID: "agt_b1",
		Model:   model.ClaudeCode,
		Mode:    task.ModePull,
		Prompt:  "hello",
	})
	if apperr.CodeOf(err) != apperr.CodeNotFound {
		t.Fatalf("cross-tenant submit must be not_found, got %v", err)
	}

	// A 提交给自己的实例 → 成功，任务带 owner。
	res, err := submit.Execute(ctx, SubmitTaskCommand{
		OwnerID: user.ID("usr_a"),
		AgentID: "agt_a1",
		Model:   model.ClaudeCode,
		Mode:    task.ModePull,
		Prompt:  "hello",
	})
	if err != nil {
		t.Fatalf("same-owner submit: %v", err)
	}
	list, _ := taskRepo.FindByOwner(ctx, user.ID("usr_a"))
	if len(list) != 1 || list[0].ID() != task.ID(res.TaskID) {
		t.Fatalf("task should be persisted under owner A")
	}
	if bList, _ := taskRepo.FindByOwner(ctx, user.ID("usr_b")); len(bList) != 0 {
		t.Fatalf("owner B should have no tasks")
	}
}
