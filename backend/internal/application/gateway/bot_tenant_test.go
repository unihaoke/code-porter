package gateway

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/user"
	infrabot "github.com/codeporter/code-porter/internal/infrastructure/bot"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	"github.com/codeporter/code-porter/pkg/apperr"
)

type botFixture struct {
	admin    *BotAdminUseCase
	registry *AgentRegistry
	taskRepo *memory.TaskRepository
	submit   *SubmitTaskUseCase
	botRepo  bot.BotRepository
}

func newBotFixture(t *testing.T) *botFixture {
	t.Helper()
	log := logging.New(io.Discard, logging.LevelError)
	clock := port.RealClock{}
	botRepo, err := infrabot.NewFileBotRepository(t.TempDir() + "/bots.json")
	if err != nil {
		t.Fatal(err)
	}
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	taskRepo := memory.NewTaskRepository()
	eventBroker := broker.NewMemoryBroker()
	policy := TaskPolicy{QueueMaxLen: 8, TTL: 200 * time.Millisecond}
	registry := NewAgentRegistry(agentRepo, queueRepo, clock, log, policy)
	submit := NewSubmitTaskUseCase(taskRepo, queueRepo, registry, eventBroker, nil, clock, log, policy)
	return &botFixture{
		admin:    NewBotAdminUseCase(botRepo, registry, clock, log),
		registry: registry,
		taskRepo: taskRepo,
		submit:   submit,
		botRepo:  botRepo,
	}
}

func actor(id user.ID, role user.Role) *user.User {
	u, _ := user.NewUser(user.Spec{ID: id, Username: string(id), PasswordHash: "x123456789", Role: role})
	return u
}

type fakeSender struct{}

func (fakeSender) Send(context.Context, *bot.Bot, bot.OutboundMessage) error { return nil }
func (fakeSender) Supports(bot.Channel) bool                                  { return true }

func TestBotAdminTenantIsolation(t *testing.T) {
	ctx := context.Background()
	f := newBotFixture(t)
	alice := actor("usr_alice", user.RoleMember)
	bob := actor("usr_bob", user.RoleMember)
	adm := actor(user.SeedAdminID, user.RoleAdmin)

	if _, err := f.registry.AuthenticateAndRegister(ctx, alice.ID(), "agt_a1", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.AuthenticateAndRegister(ctx, bob.ID(), "agt_b1", "b1"); err != nil {
		t.Fatal(err)
	}

	b1, err := f.admin.Create(ctx, alice, bot.Spec{
		Name: "alice-feishu", Channel: bot.ChannelFeishu, Enabled: true, AgentID: "agt_a1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b1.OwnerID() != alice.ID() {
		t.Fatalf("owner should be alice, got %s", b1.OwnerID())
	}

	// 绑定他人实例 → invalid_param；绑定不存在实例同样拒绝。
	if _, err := f.admin.Create(ctx, alice, bot.Spec{
		Name: "bad", Channel: bot.ChannelFeishu, AgentID: "agt_b1",
	}); apperr.CodeOf(err) != apperr.CodeInvalidParam {
		t.Fatalf("foreign agent bind must be invalid_param, got %v", err)
	}
	if _, err := f.admin.Create(ctx, alice, bot.Spec{
		Name: "bad2", Channel: bot.ChannelFeishu, AgentID: "agt_none",
	}); apperr.CodeOf(err) != apperr.CodeInvalidParam {
		t.Fatalf("nonexistent agent bind invalid_param, got %v", err)
	}

	// bob 看不到/取不到/删不掉 alice 的机器人。
	if list, err := f.admin.List(ctx, bob); err != nil || len(list) != 0 {
		t.Fatalf("bob list empty: %v %d", err, len(list))
	}
	if _, err := f.admin.Get(ctx, bob, b1.ID()); err != bot.ErrBotNotFound {
		t.Fatalf("bob get alice bot -> not found, got %v", err)
	}
	if err := f.admin.Delete(ctx, bob, b1.ID()); err != bot.ErrBotNotFound {
		t.Fatalf("bob delete alice bot -> not found, got %v", err)
	}

	// admin 全局可见可取。
	if list, err := f.admin.List(ctx, adm); err != nil || len(list) != 1 {
		t.Fatalf("admin list all: %v %d", err, len(list))
	}
	if _, err := f.admin.Get(ctx, adm, b1.ID()); err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if _, err := f.admin.List(ctx, nil); err == nil {
		t.Fatal("nil actor rejected")
	}
}

func TestBotInboundRoutingByOwner(t *testing.T) {
	ctx := context.Background()
	f := newBotFixture(t)
	log := logging.New(io.Discard, logging.LevelError)
	inbound := NewBotInboundUseCase(f.botRepo, f.submit, fakeSender{},
		model.ClaudeCode, port.RealClock{}, log, TaskPolicy{QueueMaxLen: 8, TTL: 200 * time.Millisecond})
	alice := actor("usr_alice", user.RoleMember)
	bob := actor("usr_bob", user.RoleMember)

	// alice 有两台实例且机器人未固定实例 → 回调不随机派发，409。
	for _, id := range []agent.ID{"agt_a1", "agt_a2"} {
		if _, err := f.registry.AuthenticateAndRegister(ctx, alice.ID(), id, string(id)); err != nil {
			t.Fatal(err)
		}
	}
	bMulti, err := f.admin.Create(ctx, alice, bot.Spec{Name: "multi", Channel: bot.ChannelFeishu, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inbound.Handle(ctx, bot.InboundMessage{BotID: bMulti.ID(), Text: "hi", ChatID: "c1", EventID: "e1"}); apperr.CodeOf(err) != apperr.CodeConflict {
		t.Fatalf("multi-agent inbound should conflict, got %v", err)
	}

	// bob 只有一台实例 → 回调受理成功，任务归属 bob。
	if _, err := f.registry.AuthenticateAndRegister(ctx, bob.ID(), "agt_b1", "b1"); err != nil {
		t.Fatal(err)
	}
	bBob, err := f.admin.Create(ctx, bob, bot.Spec{Name: "bob-bot", Channel: bot.ChannelFeishu, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := inbound.Handle(ctx, bot.InboundMessage{BotID: bBob.ID(), Text: "hello", ChatID: "c2", EventID: "e2"})
	if err != nil {
		t.Fatalf("single-agent inbound accepted: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("should be accepted: %+v", res)
	}
	if list, _ := f.taskRepo.FindByOwner(ctx, bob.ID()); len(list) != 1 {
		t.Fatalf("task should belong to bob")
	}
	if list, _ := f.taskRepo.FindByOwner(ctx, alice.ID()); len(list) != 0 {
		t.Fatalf("alice must have no tasks from bob callback")
	}
}
