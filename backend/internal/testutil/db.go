// Package testutil 提供跨包测试共享的环境守卫与装配辅助。
package testutil

import (
	"os"
	"testing"
)

// MySQLDSNEnv 真实 MySQL 集成测试的 DSN 环境变量名。
const MySQLDSNEnv = "CODEPORTER_TEST_MYSQL_DSN"

// RequireMySQL 无 DSN 时跳过测试并说明原因；有 DSN 时返回它。
//
// 设计目的：CI/本机没有 MySQL 时 `go test ./...` 必须保持全绿，
// MySQL 相关行为在配好数据库（或 docker compose）的环境里另跑。
func RequireMySQL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(MySQLDSNEnv)
	if dsn == "" {
		t.Skipf("skip MySQL integration test: set %s to run (e.g. root:root@tcp(127.0.0.1:3306)/codeporter_test?parseTime=true)", MySQLDSNEnv)
	}
	return dsn
}
