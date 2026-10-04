package user

import (
	"strings"
	"testing"
	"time"
)

const dummyHash = "$2a$10$abcdefghijklmnopqrstuuJ8QxQxQxQxQxQxQxQxQxQxQxQxQx"

func TestNewUserValidation(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		username string
		hash     string
		role     Role
		wantErr  bool
	}{
		{"ok member default role", "alice", dummyHash, "", false},
		{"ok admin", "admin", dummyHash, RoleAdmin, false},
		{"ok with dash underscore", "a-b_c", dummyHash, RoleMember, false},
		{"uppercase normalized", " Alice ", dummyHash, RoleMember, false}, // 去空白后全小写才合法
		{"too short", "ab", dummyHash, RoleMember, true},
		{"too long", strings.Repeat("a", 65), dummyHash, RoleMember, true},
		{"bad head char", "-alice", dummyHash, RoleMember, true},
		{"bad char", "al ice", dummyHash, RoleMember, true},
		{"empty hash", "alice", " ", RoleMember, true},
		{"invalid role", "alice", dummyHash, Role("root"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := NewUser(Spec{Username: tc.username, PasswordHash: tc.hash, Role: tc.role, Now: now})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got user %s", u.Username())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u.Status() != StatusActive || !u.CanLogin() {
				t.Fatalf("new user should be active")
			}
			if u.Username() != NormalizeUsername(tc.username) {
				t.Fatalf("username not normalized: %q", u.Username())
			}
			if u.CreatedAt() != now || u.UpdatedAt() != now {
				t.Fatalf("timestamps not applied")
			}
			if u.ID() == "" {
				t.Fatalf("id should be auto-generated")
			}
		})
	}
}

func TestNewUserFixedID(t *testing.T) {
	u, err := NewUser(Spec{ID: SeedAdminID, Username: "admin", PasswordHash: dummyHash, Role: RoleAdmin})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ID() != SeedAdminID {
		t.Fatalf("seed id mismatch: %s", u.ID())
	}
}

func TestUserBehaviors(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	u, _ := NewUser(Spec{Username: "alice", PasswordHash: dummyHash, Now: now})

	if u.IsAdmin() {
		t.Fatalf("member must not be admin")
	}
	u.Disable(now.Add(time.Minute))
	if u.CanLogin() {
		t.Fatalf("disabled user cannot login")
	}
	if u.UpdatedAt() != now.Add(time.Minute) {
		t.Fatalf("updatedAt should refresh")
	}
	u.Enable(now.Add(2 * time.Minute))
	if !u.CanLogin() {
		t.Fatalf("enabled user can login")
	}

	newHash := "$2a$10$0000000000000000000000000000000000000000000000000000"
	if err := u.ChangePassword(newHash, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if u.PasswordHash() != newHash {
		t.Fatalf("password hash not updated")
	}
	if err := u.ChangePassword(" ", now); err == nil {
		t.Fatalf("empty hash must be rejected")
	}
	if err := u.ChangeRole(Role("root"), now); err == nil {
		t.Fatalf("invalid role must be rejected")
	}
	if err := u.ChangeRole(RoleAdmin, now); err != nil {
		t.Fatalf("change role: %v", err)
	}
	if !u.IsAdmin() {
		t.Fatalf("role should be admin now")
	}
}

func TestUserCloneIsolation(t *testing.T) {
	u, _ := NewUser(Spec{Username: "alice", PasswordHash: dummyHash})
	cp := u.Clone()
	cp.Disable(time.Now())
	if !u.CanLogin() {
		t.Fatalf("mutating clone must not affect original")
	}
}

func TestRewrite(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	u := &User{}
	u.Rewrite("usr_x", "bob", dummyHash, RoleMember, StatusDisabled, created, updated)
	if u.ID() != "usr_x" || u.Username() != "bob" || u.Status() != StatusDisabled ||
		u.CreatedAt() != created || u.UpdatedAt() != updated {
		t.Fatalf("rewrite did not restore fields: %+v", u)
	}
}
