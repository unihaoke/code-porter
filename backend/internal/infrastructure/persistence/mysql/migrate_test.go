package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/security"
	"github.com/codeporter/code-porter/internal/testutil"
)

// TestMigrateIdempotentAndSeed 验证：迁移幂等、5 张表齐全、种子 admin 存在且默认密码可校验。
func TestMigrateIdempotentAndSeed(t *testing.T) {
	dsn := testutil.RequireMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := Open(ctx, dsn, PoolConfig{MaxOpenConns: 5, MaxIdleConns: 2, ConnMaxLifetime: time.Minute})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, dsn, pool); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(ctx, dsn, pool); err != nil {
		t.Fatalf("second migrate should be idempotent: %v", err)
	}

	wantTables := map[string]bool{
		"users": false, "api_keys": false, "sessions": false,
		"agents": false, "schema_migrations": false,
	}
	rows, err := pool.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = DATABASE()`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if _, ok := wantTables[name]; ok {
			wantTables[name] = true
		}
	}
	rows.Close()
	for name, found := range wantTables {
		if !found {
			t.Fatalf("table %s missing", name)
		}
	}

	var id, username, hash, role, status string
	err = pool.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, status FROM users WHERE username = 'admin'`).
		Scan(&id, &username, &hash, &role, &status)
	if err != nil {
		t.Fatalf("query seed admin: %v", err)
	}
	if id != string(user.SeedAdminID) {
		t.Fatalf("seed admin id mismatch: %s", id)
	}
	if role != "admin" || status != "active" {
		t.Fatalf("seed admin role/status mismatch: %s/%s", role, status)
	}
	if !security.NewBcryptHasher().Compare(hash, "admin123") {
		t.Fatalf("seed admin hash must verify against admin123")
	}
	if security.NewBcryptHasher().Compare(hash, "wrong-password") {
		t.Fatalf("seed admin hash must not verify wrong password")
	}

	var count int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = 'admin'`).Scan(&count); err != nil {
		t.Fatalf("count admin: %v", err)
	}
	if count != 1 {
		t.Fatalf("seed admin must be exactly 1 row, got %d", count)
	}

	var migrations int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrations < 1 {
		t.Fatalf("schema_migrations should record at least 1 migration, got %d", migrations)
	}
}
