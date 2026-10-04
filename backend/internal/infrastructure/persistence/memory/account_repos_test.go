package memory

import (
	"context"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

func TestUserRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	r := NewUserRepository()

	u, err := user.NewUser(user.Spec{Username: "alice", PasswordHash: "h1"})
	if err != nil {
		t.Fatalf("new user: %v", err)
	}
	if err := r.Save(ctx, u); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.FindByUsername(ctx, "ALICE ")
	if err != nil {
		t.Fatalf("find normalized username: %v", err)
	}
	if got.ID() != u.ID() {
		t.Fatalf("id mismatch")
	}
	if _, err := r.FindByID(ctx, u.ID()); err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if n, _ := r.Count(ctx); n != 1 {
		t.Fatalf("count = %d", n)
	}

	// 重名（不同 ID）拒绝。
	u2, _ := user.NewUser(user.Spec{Username: "alice", PasswordHash: "h2"})
	if err := r.Save(ctx, u2); err != user.ErrDuplicateUsername {
		t.Fatalf("duplicate username expected, got %v", err)
	}

	// 改名后旧名字应释放。
	got.ChangePassword("h9", time.Now())
	_ = got.ChangeRole(user.RoleAdmin, time.Now())
	if err := r.Save(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := r.FindByUsername(ctx, "alice"); err != nil {
		t.Fatalf("renamed? role change keeps username; still found: %v", err)
	}

	if err := r.Delete(ctx, u.ID()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.FindByID(ctx, u.ID()); err != user.ErrUserNotFound {
		t.Fatalf("not found after delete expected, got %v", err)
	}
}

func TestAPIKeyRepositoryContract(t *testing.T) {
	ctx := context.Background()
	r := NewAPIKeyRepository()
	future := time.Now().Add(time.Hour)

	k, err := apikey.NewKey(apikey.Spec{
		UserID:    user.ID("usr_a"),
		Name:      "laptop",
		Scopes:    apikey.AllScopes,
		KeyHash:   "a000000000000000000000000000000000000000000000000000000000000001",
		Prefix:    "cp_aaaaaaa1",
		ExpiresAt: &future,
	})
	if err != nil {
		t.Fatalf("new key: %v", err)
	}
	if err := r.Save(ctx, k); err != nil {
		t.Fatalf("save: %v", err)
	}
	byHash, err := r.FindByHash(ctx, k.KeyHash())
	if err != nil || byHash.ID() != k.ID() {
		t.Fatalf("find by hash: %v", err)
	}
	list, _ := r.ListByUser(ctx, user.ID("usr_a"))
	if len(list) != 1 {
		t.Fatalf("owner list = %d", len(list))
	}
	if list, _ := r.ListByUser(ctx, user.ID("usr_b")); len(list) != 0 {
		t.Fatalf("foreign owner list must be empty")
	}
	all, _ := r.ListAll(ctx)
	if len(all) != 1 {
		t.Fatalf("list all = %d", len(all))
	}
	if err := r.Delete(ctx, k.ID()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.FindByID(ctx, k.ID()); err != apikey.ErrAPIKeyNotFound {
		t.Fatalf("not found expected, got %v", err)
	}
}

func TestSessionRepositoryContract(t *testing.T) {
	ctx := context.Background()
	r := NewSessionRepository()
	now := time.Now()

	s1, err := user.NewSession(user.SessionSpec{TokenHash: "hash1", UserID: user.ID("usr_a"), Now: now, TTL: time.Hour})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s2, _ := user.NewSession(user.SessionSpec{TokenHash: "hash2", UserID: user.ID("usr_a"), Now: now, TTL: time.Hour})
	s3, _ := user.NewSession(user.SessionSpec{TokenHash: "hash3", UserID: user.ID("usr_b"), Now: now, TTL: time.Hour})
	for _, s := range []*user.Session{s1, s2, s3} {
		if err := r.Save(ctx, s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	if got, err := r.FindByTokenHash(ctx, "hash1"); err != nil || got.UserID() != "usr_a" {
		t.Fatalf("find session: %v %v", got, err)
	}

	// 改密场景：删除 usr_a 全部会话但保留当前 hash1。
	if err := r.DeleteByUserExcept(ctx, user.ID("usr_a"), "hash1"); err != nil {
		t.Fatalf("delete by user: %v", err)
	}
	if _, err := r.FindByTokenHash(ctx, "hash1"); err != nil {
		t.Fatalf("current session should remain: %v", err)
	}
	if _, err := r.FindByTokenHash(ctx, "hash2"); err != user.ErrSessionNotFound {
		t.Fatalf("other session should be deleted, got %v", err)
	}
	if _, err := r.FindByTokenHash(ctx, "hash3"); err != nil {
		t.Fatalf("other user session must be untouched: %v", err)
	}

	// 过期清理。
	expired, _ := user.NewSession(user.SessionSpec{TokenHash: "hash4", UserID: user.ID("usr_b"), Now: now.Add(-2 * time.Hour), TTL: time.Hour})
	_ = r.Save(ctx, expired)
	if err := r.DeleteExpired(ctx, now); err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if _, err := r.FindByTokenHash(ctx, "hash4"); err != user.ErrSessionNotFound {
		t.Fatalf("expired session should be purged, got %v", err)
	}

	if err := r.Delete(ctx, "hash1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
