package security

import (
	"regexp"
	"strings"
	"testing"
)

func TestBcryptHasher(t *testing.T) {
	h := NewBcryptHasher()
	hash, err := h.Hash("admin123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("not a bcrypt hash: %s", hash)
	}
	if hash == "admin123" {
		t.Fatalf("hash must not equal plaintext")
	}
	if !h.Compare(hash, "admin123") {
		t.Fatalf("correct password should compare true")
	}
	if h.Compare(hash, "wrong") {
		t.Fatalf("wrong password must compare false")
	}
	if h.Compare("", "admin123") || h.Compare(hash, "") {
		t.Fatalf("empty inputs must be false")
	}
	// 同一密码两次哈希应不同（bcrypt 随机盐）。
	hash2, _ := h.Hash("admin123")
	if hash == hash2 {
		t.Fatalf("bcrypt salts should differ")
	}
}

func TestRandomGeneratorAPIKey(t *testing.T) {
	g := NewRandomGenerator()
	pattern := regexp.MustCompile(`^cp_[A-Za-z0-9_-]{43}$`)
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		k, err := g.APIKey()
		if err != nil {
			t.Fatalf("gen api key: %v", err)
		}
		if !pattern.MatchString(k) {
			t.Fatalf("api key shape mismatch: %q (len=%d)", k, len(k))
		}
		if _, dup := seen[k]; dup {
			t.Fatalf("api key duplicated at iteration %d", i)
		}
		seen[k] = struct{}{}
	}
}

func TestRandomGeneratorSessionToken(t *testing.T) {
	g := NewRandomGenerator()
	tok1, err := g.SessionToken()
	if err != nil {
		t.Fatalf("gen session token: %v", err)
	}
	if len(tok1) != 64 {
		t.Fatalf("session token should be 64 hex chars, got %d", len(tok1))
	}
	tok2, _ := g.SessionToken()
	if tok1 == tok2 {
		t.Fatalf("session tokens must differ")
	}
}

func TestSHA256Hasher(t *testing.T) {
	h := NewSHA256Hasher()
	const secret = "cp_abcdef"
	got := h.Hash(secret)
	if len(got) != 64 {
		t.Fatalf("sha256 hex should be 64 chars, got %d", len(got))
	}
	if got == secret {
		t.Fatalf("hash must not equal plaintext")
	}
	// 确定性。
	if h.Hash(secret) != got {
		t.Fatalf("hash should be deterministic")
	}
	if !h.EqualHash(got, secret) {
		t.Fatalf("equal hash should pass")
	}
	if h.EqualHash(got, "cp_other") {
		t.Fatalf("different secret must fail")
	}
	if h.EqualHash("", secret) || h.EqualHash(got, "") {
		t.Fatalf("empty inputs must be false")
	}
}
