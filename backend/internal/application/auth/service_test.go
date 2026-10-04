package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	"github.com/codeporter/code-porter/internal/infrastructure/security"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newTestService(t *testing.T) (*Service, *memory.UserRepository) {
	t.Helper()
	users := memory.NewUserRepository()
	svc := NewService(Deps{
		Users:      users,
		Keys:       memory.NewAPIKeyRepository(),
		Sessions:   memory.NewSessionRepository(),
		Hasher:     security.NewBcryptHasher(),
		Secrets:    security.NewSHA256Hasher(),
		Generator:  security.NewRandomGenerator(),
		Clock:      fixedClock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)},
		SessionTTL: time.Hour,
	})
	admin, err := user.NewUser(user.Spec{ID: user.SeedAdminID, Username: "admin", PasswordHash: mustHash("admin123"), Role: user.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Save(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	return svc, users
}

func mustHash(pw string) string {
	h, err := security.NewBcryptHasher().Hash(pw)
	if err != nil {
		panic(err)
	}
	return h
}

func TestLoginSuccessAndSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	res, err := svc.Login(ctx, "ADMIN", "admin123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Token == "" || res.ExpiresAt.IsZero() {
		t.Fatalf("token/expiry mismatch: %+v", res)
	}
	if res.User.Username != "admin" || res.User.Role != "admin" {
		t.Fatalf("user view mismatch: %+v", res.User)
	}

	me, err := svc.AuthenticateSession(ctx, res.Token)
	if err != nil || me.Username() != "admin" {
		t.Fatalf("session auth: %v", err)
	}
	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.AuthenticateSession(ctx, res.Token); err == nil {
		t.Fatalf("session must be invalid after logout")
	}
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, errMissing := svc.Login(ctx, "ghost", "whatever")
	_, errWrongPW := svc.Login(ctx, "admin", "wrong")
	if errMissing == nil || errWrongPW == nil {
		t.Fatal("both should fail")
	}
	if errMissing.Error() != errWrongPW.Error() {
		t.Fatalf("error messages must be identical to prevent enumeration: %q vs %q",
			errMissing.Error(), errWrongPW.Error())
	}
	if _, err := svc.AuthenticateSession(ctx, ""); err == nil {
		t.Fatal("empty token rejected")
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	u, _ := users.FindByUsername(ctx, "admin")
	u.Disable(time.Now())
	_ = users.Save(ctx, u)
	if _, err := svc.Login(ctx, "admin", "admin123"); err == nil {
		t.Fatal("disabled user must not login")
	}
}

func TestUserAdminRBAC(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)

	member, err := svc.CreateUser(ctx, admin, "bob", "secret123", "member")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	actor, _ := users.FindByID(ctx, user.ID(member.ID))

	// member 不能做用户管理。
	if _, err := svc.ListUsers(ctx, actor); err == nil {
		t.Fatal("member must not list users")
	}
	if _, err := svc.CreateUser(ctx, actor, "c", "secret123", "member"); err == nil {
		t.Fatal("member must not create user")
	}
	if err := svc.DeleteUser(ctx, actor, user.SeedAdminID); err == nil {
		t.Fatal("member must not delete users")
	}

	// admin 不能删自己。
	if err := svc.DeleteUser(ctx, admin, admin.ID()); err == nil {
		t.Fatal("admin cannot delete self")
	}
	// 重名冲突。
	if _, err := svc.CreateUser(ctx, admin, "BOB", "secret123", "member"); err == nil {
		t.Fatal("duplicate username must conflict (case-insensitive)")
	}
	// 非法角色/弱密码。
	if _, err := svc.CreateUser(ctx, admin, "carol", "secret123", "root"); err == nil {
		t.Fatal("invalid role rejected")
	}
	if _, err := svc.CreateUser(ctx, admin, "dave", "123", "member"); err == nil {
		t.Fatal("short password rejected")
	}
	// admin 删除 bob 成功，且其秘钥/会话随之级联（存储层外键；内存仓储仅删用户行）。
	if err := svc.DeleteUser(ctx, admin, actor.ID()); err != nil {
		t.Fatalf("admin delete member: %v", err)
	}
	if _, err := svc.ListUsers(ctx, admin); err != nil {
		t.Fatalf("list users after delete: %v", err)
	}
}

func TestPasswordChangeInvalidatesOtherSessions(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	res1, err := svc.Login(ctx, "admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	res2, err := svc.Login(ctx, "admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := svc.AuthenticateSession(ctx, res1.Token)
	hash1 := svc.deps.Secrets.Hash(res1.Token)
	if err := svc.ChangePassword(ctx, admin, "admin123", "newpass123", hash1); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := svc.AuthenticateSession(ctx, res1.Token); err != nil {
		t.Fatal("current session must remain")
	}
	if _, err := svc.AuthenticateSession(ctx, res2.Token); err == nil {
		t.Fatal("other sessions must be invalid after password change")
	}
	// 旧密码登录失败，新密码成功。
	if _, err := svc.Login(ctx, "admin", "admin123"); err == nil {
		t.Fatal("old password must fail")
	}
	if _, err := svc.Login(ctx, "admin", "newpass123"); err != nil {
		t.Fatalf("new password must work: %v", err)
	}
}

func TestResetPasswordKillsSessions(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)
	bob, err := svc.CreateUser(ctx, admin, "bob", "secret123", "member")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Login(ctx, "bob", "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(ctx, admin, user.ID(bob.ID), "rotated123"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := svc.AuthenticateSession(ctx, res.Token); err == nil {
		t.Fatal("reset must invalidate sessions")
	}
	if _, err := svc.Login(ctx, "bob", "rotated123"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)

	created, err := svc.CreateKey(ctx, admin, "laptop", nil, "", nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(created.Secret, "cp_") || len(created.Secret) < 36 {
		t.Fatalf("secret shape: %q", created.Secret)
	}
	if created.Prefix != created.Secret[:apikey.PrefixLen] {
		t.Fatalf("prefix mismatch")
	}
	if len(created.Scopes) != 2 {
		t.Fatalf("default scopes should be both, got %v", created.Scopes)
	}
	// 文件权限缺省 all。
	if created.Permission != string(apikey.PermissionAll) {
		t.Fatalf("default permission = %q, want all", created.Permission)
	}

	// 列表不含明文。
	list, err := svc.ListKeys(ctx, admin, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), created.Secret) {
		t.Fatalf("list must not leak secret: %s", raw)
	}

	// 明文可以鉴权（默认双 scope）。
	owner, k, err := svc.AuthenticateAPISecret(ctx, created.Secret, apikey.ScopeAPI)
	if err != nil {
		t.Fatalf("api auth: %v", err)
	}
	if owner.ID() != admin.ID() || k.ID() == "" {
		t.Fatalf("auth result mismatch")
	}
	if _, _, err := svc.AuthenticateAPISecret(ctx, created.Secret, apikey.ScopeAgent); err != nil {
		t.Fatalf("agent scope should also work: %v", err)
	}

	// 删除后立即失效。
	if err := svc.DeleteKey(ctx, admin, "", apikey.ID(created.ID)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := svc.AuthenticateAPISecret(ctx, created.Secret, apikey.ScopeAPI); err == nil {
		t.Fatal("deleted key must fail")
	}
}

func TestKeyScopeMatrixAndExpiry(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)

	apiOnly, err := svc.CreateKey(ctx, admin, "api-only", []string{"api"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AuthenticateAPISecret(ctx, apiOnly.Secret, apikey.ScopeAPI); err != nil {
		t.Fatalf("api scope should pass: %v", err)
	}
	if _, _, err := svc.AuthenticateAPISecret(ctx, apiOnly.Secret, apikey.ScopeAgent); err == nil {
		t.Fatal("api-only key must be denied agent scope")
	}

	past := svc.deps.Clock.Now().Add(-time.Minute)
	if _, err := svc.CreateKey(ctx, admin, "expired", nil, "", &past); err == nil {
		t.Fatal("past expiry rejected at creation")
	}
	future := svc.deps.Clock.Now().Add(time.Minute)
	expKey, err := svc.CreateKey(ctx, admin, "soonexpire", nil, "", &future)
	if err != nil {
		t.Fatal(err)
	}
	// 把时钟拨到过期后。
	svc.deps.Clock = fixedClock{t: future.Add(time.Second)}
	if _, _, err := svc.AuthenticateAPISecret(ctx, expKey.Secret, apikey.ScopeAPI); err == nil {
		t.Fatal("expired key must be rejected")
	}
}

func TestKeyCrossTenant(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)
	bob, err := svc.CreateUser(ctx, admin, "bob", "secret123", "member")
	if err != nil {
		t.Fatal(err)
	}
	bobActor, _ := users.FindByID(ctx, user.ID(bob.ID))
	ck, err := svc.CreateKey(ctx, admin, "admin-key", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// bob 不能删 admin 的秘钥：指定他人 ID 需要 admin。
	if err := svc.DeleteKey(ctx, bobActor, admin.ID(), apikey.ID(ck.ID)); err == nil {
		t.Fatal("member must not delete other user's key")
	}
	// bob 列自己的秘钥看不到 admin 的。
	list, err := svc.ListKeys(ctx, bobActor, "")
	if err != nil || len(list) != 0 {
		t.Fatalf("bob should see no keys: %v %d", err, len(list))
	}
	// admin 代管：可以列/删 bob 将要创建的秘钥。
	bk, err := svc.CreateKey(ctx, bobActor, "bob-key", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	listAdmin, err := svc.ListKeys(ctx, admin, bobActor.ID())
	if err != nil || len(listAdmin) != 1 {
		t.Fatalf("admin lists bob keys: %v %d", err, len(listAdmin))
	}
	if err := svc.DeleteKey(ctx, admin, bobActor.ID(), apikey.ID(bk.ID)); err != nil {
		t.Fatalf("admin delete bob key: %v", err)
	}
}

func TestIsUsingDefaultPassword(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t) // 夹具里的种子 admin 密码就是 admin123
	if !svc.IsUsingDefaultPassword(ctx) {
		t.Fatal("seed admin with admin123 should be detected")
	}
	admin, _ := users.FindByID(ctx, user.SeedAdminID)
	if err := admin.ChangePassword(mustHash("changed-pass-1"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := users.Save(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if svc.IsUsingDefaultPassword(ctx) {
		t.Fatal("after changing seed password detection should be false")
	}
}

func TestViewsNeverContainSecrets(t *testing.T) {
	ctx := context.Background()
	svc, users := newTestService(t)
	admin, _ := users.FindByID(ctx, user.SeedAdminID)
	login, err := svc.Login(ctx, "admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"password", "hash", "token_hash", "password_hash"}
	body, _ := json.Marshal(login.User)
	for _, bad := range forbidden {
		if strings.Contains(strings.ToLower(string(body)), bad) {
			t.Fatalf("user view leaks %q: %s", bad, body)
		}
	}
	ck, err := svc.CreateKey(ctx, admin, "v", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	kv, _ := json.Marshal(ck.KeyView)
	for _, bad := range []string{"hash", "secret"} {
		if strings.Contains(strings.ToLower(string(kv)), bad) {
			t.Fatalf("key view leaks %q: %s", bad, kv)
		}
	}
}
