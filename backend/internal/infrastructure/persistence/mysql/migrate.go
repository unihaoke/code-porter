package mysql

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"


	"github.com/go-sql-driver/mysql"
)

// migrationsFS 内嵌版本化 SQL（文件名即版本号，按字典序执行）。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate 执行未应用的迁移；每个文件整体执行（独立连接开启 multiStatements），
// 成功后在 schema_migrations 记账。DDL 在 MySQL 中隐式提交，因此不显式包事务。
func Migrate(ctx context.Context, appDSN string, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version     VARCHAR(128) NOT NULL PRIMARY KEY,
		applied_at  DATETIME(6) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]struct{}{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = struct{}{}
	}
	rows.Close()

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	migDB, err := openMigrationConn(appDSN)
	if err != nil {
		return err
	}
	defer migDB.Close()

	for _, name := range names {
		if _, ok := applied[name]; ok {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := migDB.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, time.Now().UTC()); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

// openMigrationConn 单独开一个允许多语句的连接，仅供迁移使用。
func openMigrationConn(appDSN string) (*sql.DB, error) {
	parsed, err := mysql.ParseDSN(appDSN)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn for migration: %w", err)
	}
	parsed.ParseTime = true
	parsed.MultiStatements = true
	db, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
