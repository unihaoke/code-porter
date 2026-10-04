package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/security"
	"github.com/codeporter/code-porter/internal/testutil"
)

// TestRepositoriesCRUDAndCascade 在真实 MySQL 上跑一遍五仓储的关键契约与外键级联。
// 无 CODEPORTER_TEST_MYSQL_DSN 时自动跳过。
func TestRepositoriesCRUDAndCascade(t *testing.T) {
	dsn := testutil.RequireMySQL(t)
	ctx := context.Background()

	pool, err := Open(ctx, dsn, PoolConfig{MaxOpenConns: 5, MaxIdleConns: 2})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, dsn, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	userRepo := NewUserRepository(pool)
	keyRepo := NewAPIKeyRepository(pool)
	sessRepo := NewSessionRepository(pool)
	agentRepo := NewAgentRepository(pool)
	botRepo := NewBotRepository(pool)

	// 隔离：每个用例使用独特用户名，结尾统一清理；种子 admin 不动。
	owner, err := user.NewUser(user.Spec{
		Username: "t6_crud_user", PasswordHash: mustHash(t, "pw-1"), Role: user.RoleMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 幂等重跑：存在则复用。
	if existing, ferr := userRepo.FindByUsername(ctx, owner.Username()); ferr == nil {
		owner = existing
	} else if err := userRepo.Save(ctx, owner); err != nil {
		t.Fatalf("save owner: %v", err)
	}
	t.Cleanup(func() { _ = userRepo.Delete(ctx, owner.ID()) })

	// --- users ---
	dup, _ := user.NewUser(user.Spec{Username: "t6_crud_user", PasswordHash: "x"})
	if err := userRepo.Save(ctx, dup); err != user.ErrDuplicateUsername {
		t.Fatalf("duplicate username should conflict, got %v", err)
	}
	got, err := userRepo.FindByUsername(ctx, owner.Username())
	if err != nil || got.ID() != owner.ID() {
		t.Fatalf("find user: %v", err)
	}

	// --- api keys ---
	gen := security.NewRandomGenerator()
	secret, err := gen.APIKey()
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(24 * time.Hour)
	key, err := apikey.NewKey(apikey.Spec{
		UserID: owner.ID(), Name: "t6", Scopes: apikey.AllScopes,
		KeyHash: security.NewSHA256Hasher().Hash(secret), Prefix: secret[:apikey.PrefixLen],
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := keyRepo.Save(ctx, key); err != nil {
		t.Fatalf("save key: %v", err)
	}
	byHash, err := keyRepo.FindByHash(ctx, key.KeyHash())
	if err != nil || byHash.OwnerID() != owner.ID() || !byHash.HasExpiry() {
		t.Fatalf("find key: %v", err)
	}
	if len(byHash.Scopes()) != 2 {
		t.Fatalf("scopes roundtrip: %v", byHash.Scopes())
	}
	keys, err := keyRepo.ListByUser(ctx, owner.ID())
	if err != nil || len(keys) != 1 {
		t.Fatalf("list by owner = %d, err %v", len(keys), err)
	}

	// --- sessions ---
	tok, err := gen.SessionToken()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := user.NewSession(user.SessionSpec{
		TokenHash: security.NewSHA256Hasher().Hash(tok), UserID: owner.ID(), TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Save(ctx, sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if _, err := sessRepo.FindByTokenHash(ctx, sess.TokenHash()); err != nil {
		t.Fatalf("find session: %v", err)
	}

	// --- agents ---
	ag, err := agent.Register(agent.Spec{ID: "agt_t6crud", OwnerID: owner.ID(), Name: "pc-1", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentRepo.Save(ctx, ag); err != nil {
		t.Fatalf("save agent: %v", err)
	}
	ag.UpdateName("pc-renamed")
	ag.SetLimits(10, 0)
	if err := agentRepo.Save(ctx, ag); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	gotAg, err := agentRepo.FindByIdentity(ctx, owner.ID(), ag.ID())
	if err != nil {
		t.Fatalf("find identity: %v", err)
	}
	if gotAg.Name() != "pc-renamed" {
		t.Fatalf("agent name should update, got %s", gotAg.Name())
	}
	if _, err := agentRepo.FindByIdentity(ctx, user.ID("usr_someone_else"), ag.ID()); err != agent.ErrAgentNotFound {
		t.Fatalf("foreign owner must not see agent, got %v", err)
	}

	// --- bots ---
	b, err := bot.NewBot(bot.Spec{
		OwnerID: owner.ID(), Name: "t6-bot", Channel: bot.ChannelFeishu, Enabled: true,
		Model: model.ClaudeCode, Mode: task.ModePull, AgentID: string(ag.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := botRepo.Save(ctx, b); err != nil {
		t.Fatalf("save bot: %v", err)
	}
	bots, err := botRepo.FindByOwner(ctx, owner.ID())
	if err != nil || len(bots) != 1 {
		t.Fatalf("find bots by owner: %v %d", err, len(bots))
	}
	// --- cascade ---
	if err := userRepo.Delete(ctx, owner.ID()); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := keyRepo.FindByID(ctx, key.ID()); err != apikey.ErrAPIKeyNotFound {
		t.Fatalf("key should cascade-delete, got %v", err)
	}
	if _, err := sessRepo.FindByTokenHash(ctx, sess.TokenHash()); err != user.ErrSessionNotFound {
		t.Fatalf("session should cascade-delete, got %v", err)
	}
	if _, err := agentRepo.Find(ctx, ag.ID()); err != agent.ErrAgentNotFound {
		t.Fatalf("agent should cascade-delete, got %v", err)
	}
	if left, _ := botRepo.FindByOwner(ctx, owner.ID()); len(left) != 0 {
		t.Fatalf("bots should cascade-delete, got %d", len(left))
	}
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := security.NewBcryptHasher().Hash(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}
