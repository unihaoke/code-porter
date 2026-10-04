package mysql

import (
	"regexp"
	"strings"
	"testing"

	"github.com/codeporter/code-porter/internal/infrastructure/security"
)

// TestSeedHashWithoutDB 直接校验迁移 SQL 内联的种子哈希与 admin123 匹配，
// 避免在没有 MySQL 的环境里内联错误哈希（TR-5.2）。
func TestSeedHashWithoutDB(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read embedded migration: %v", err)
	}
	sql := string(raw)

	rx := regexp.MustCompile(`'\$2[ab]\$10\$[./A-Za-z0-9]{53}'`)
	quoted := rx.FindString(sql)
	if quoted == "" {
		t.Fatal("bcrypt seed hash not found in 0001_init.sql")
	}
	hash := strings.Trim(quoted, "'")
	if len(hash) != 60 {
		t.Fatalf("bcrypt hash length should be 60, got %d", len(hash))
	}
	if !strings.Contains(sql, string(`'usr_admin_seed'`)) || !strings.Contains(sql, "INSERT IGNORE") {
		t.Fatal("seed insert must be INSERT IGNORE with fixed seed id")
	}

	hasher := security.NewBcryptHasher()
	if !hasher.Compare(hash, "admin123") {
		t.Fatal("embedded seed hash must verify against admin123")
	}
	if hasher.Compare(hash, "admin1234") {
		t.Fatal("embedded seed hash must not verify a different password")
	}
}
