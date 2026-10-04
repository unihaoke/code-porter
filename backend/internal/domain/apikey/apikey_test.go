package apikey

import (
	"strings"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
)

const dummyHash64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func baseSpec(now time.Time) Spec {
	return Spec{
		UserID:  user.ID("usr_alice"),
		Name:    "my laptop",
		Scopes:  AllScopes,
		KeyHash: dummyHash64,
		Prefix:  "cp_abcd1234",
		Now:     now,
	}
}

func TestNewKeyValidation(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)

	t.Run("ok defaults", func(t *testing.T) {
		k, err := NewKey(baseSpec(now))
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if k.ID() == "" || !k.HasScope(ScopeAgent) || !k.HasScope(ScopeAPI) {
			t.Fatalf("id/scopes mismatch: %+v", k)
		}
		if k.HasExpiry() {
			t.Fatalf("nil expiry means never expire")
		}
	})

	t.Run("missing owner", func(t *testing.T) {
		s := baseSpec(now)
		s.UserID = ""
		if _, err := NewKey(s); err != ErrEmptyOwner {
			t.Fatalf("want ErrEmptyOwner, got %v", err)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		s := baseSpec(now)
		s.Name = "   "
		if _, err := NewKey(s); err != ErrEmptyName {
			t.Fatalf("want ErrEmptyName, got %v", err)
		}
	})

	t.Run("name too long", func(t *testing.T) {
		s := baseSpec(now)
		s.Name = strings.Repeat("中", maxNameLen+1)
		if _, err := NewKey(s); err != ErrNameTooLong {
			t.Fatalf("want ErrNameTooLong, got %v", err)
		}
	})

	t.Run("empty scopes", func(t *testing.T) {
		s := baseSpec(now)
		s.Scopes = nil
		if _, err := NewKey(s); err != ErrInvalidScope {
			t.Fatalf("want ErrInvalidScope, got %v", err)
		}
	})

	t.Run("invalid scope", func(t *testing.T) {
		s := baseSpec(now)
		s.Scopes = []Scope{"sudo"}
		if _, err := NewKey(s); err != ErrInvalidScope {
			t.Fatalf("want ErrInvalidScope, got %v", err)
		}
	})

	t.Run("scopes dedup", func(t *testing.T) {
		s := baseSpec(now)
		s.Scopes = []Scope{ScopeAPI, ScopeAPI, ScopeAgent}
		k, err := NewKey(s)
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if len(k.Scopes()) != 2 {
			t.Fatalf("scopes should be deduplicated, got %v", k.Scopes())
		}
	})

	t.Run("bad hash length", func(t *testing.T) {
		s := baseSpec(now)
		s.KeyHash = "abc"
		if _, err := NewKey(s); err != ErrEmptyHash {
			t.Fatalf("want ErrEmptyHash, got %v", err)
		}
	})

	t.Run("empty prefix", func(t *testing.T) {
		s := baseSpec(now)
		s.Prefix = ""
		if _, err := NewKey(s); err != ErrEmptyPrefix {
			t.Fatalf("want ErrEmptyPrefix, got %v", err)
		}
	})

	t.Run("expiry in past", func(t *testing.T) {
		s := baseSpec(now)
		s.ExpiresAt = &past
		if _, err := NewKey(s); err != ErrExpiresInPast {
			t.Fatalf("want ErrExpiresInPast, got %v", err)
		}
	})

	t.Run("future expiry ok", func(t *testing.T) {
		s := baseSpec(now)
		future := now.Add(24 * time.Hour)
		s.ExpiresAt = &future
		k, err := NewKey(s)
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if !k.HasExpiry() || !k.ExpiresAt().Equal(future) {
			t.Fatalf("expiry mismatch")
		}
	})
}

func TestUsableAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Minute)

	agentOnly := baseSpec(now)
	agentOnly.Scopes = []Scope{ScopeAgent}
	k, err := NewKey(agentOnly)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if k.Usable(now, ScopeAgent) != true {
		t.Fatalf("agent scope should be usable")
	}
	if k.Usable(now, ScopeAPI) != false {
		t.Fatalf("api scope must be denied")
	}

	expiring := baseSpec(now)
	expiring.ExpiresAt = &future
	ek, _ := NewKey(expiring)
	if ek.Expired(now) {
		t.Fatalf("not expired yet")
	}
	if !ek.Usable(now.Add(30*time.Minute), ScopeAgent) {
		t.Fatalf("should be usable before expiry")
	}
	if ek.Usable(now.Add(2*time.Hour), ScopeAgent) {
		t.Fatalf("must be unusable after expiry")
	}
	if !ek.Expired(now.Add(2 * time.Hour)) {
		t.Fatalf("expired expected")
	}

	// 边界：过期时刻即视为过期（!Before）。
	if !ek.Expired(future) {
		t.Fatalf("at expiry instant should be treated expired")
	}
	_ = past
}

func TestMarkUsed(t *testing.T) {
	now := time.Now()
	k, _ := NewKey(baseSpec(now))
	if !k.LastUsedAt().IsZero() {
		t.Fatalf("new key never used")
	}
	used := now.Add(time.Minute)
	k.MarkUsed(used)
	if !k.LastUsedAt().Equal(used) {
		t.Fatalf("last_used_at mismatch")
	}
}

func TestCloneIsolation(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	s := baseSpec(now)
	s.ExpiresAt = &future
	k, _ := NewKey(s)

	cp := k.Clone()
	cp.Scopes()[0] = Scope("hacked")
	cp.ExpiresAt() // 访问器返回值副本不影响内部
	if len(k.Scopes()) != 2 || k.Scopes()[0] != ScopeAgent {
		t.Fatalf("clone scope mutation leaked to original: %v", k.Scopes())
	}

	// 修改副本的过期时间指针不影响原对象。
	cpTime := cp.ExpiresAt().Add(48 * time.Hour)
	cp.Rewrite(cp.ID(), cp.OwnerID(), cp.Name(), cp.Scopes(), cp.KeyHash(), cp.Prefix(), &cpTime, cp.LastUsedAt(), cp.CreatedAt(), cp.UpdatedAt())
	if k.ExpiresAt().Equal(cpTime) {
		t.Fatalf("clone expiry pointer mutation leaked")
	}
}
