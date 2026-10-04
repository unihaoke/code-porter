// Package mysql 提供网关的 MySQL 持久化：连接池、版本化迁移与各聚合仓储实现。
//
// 运行时态（任务队列、在线状态、事件 broker）仍在内存；本包只承载
// users / api_keys / sessions / agents / bots 这五类需要持久化的数据。
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

// PoolConfig 连接池参数。
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Open 打开连接池并带重试地 Ping，直到成功或 ctx 超时。
//
// 强制 parseTime=true（datetime(6) 扫描为 time.Time）与 utf8mb4；
// 应用连接池不开启 multiStatements（迁移使用独立连接）。
func Open(ctx context.Context, dsn string, cfg PoolConfig) (*sql.DB, error) {
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn: %w", err)
	}
	parsed.ParseTime = true
	if parsed.Params == nil {
		parsed.Params = map[string]string{}
	}
	if _, ok := parsed.Params["charset"]; !ok {
		parsed.Params["charset"] = "utf8mb4"
	}

	db, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	if err := pingWithRetry(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// pingWithRetry 网关常先于 MySQL 就绪（compose 已排序健康检查，这里再兜底）：
// 立即试一次，之后每 2s 重试，直到 ctx 截止。
func pingWithRetry(ctx context.Context, db *sql.DB) error {
	attempt := 0
	for {
		err := db.PingContext(ctx)
		if err == nil {
			if attempt > 0 {
				// 日志由调用方记录；此处仅返回结果。
			}
			return nil
		}
		attempt++
		select {
		case <-ctx.Done():
			return fmt.Errorf("mysql unreachable after %d attempts: %w (last error: %v)", attempt, ctx.Err(), err)
		case <-time.After(2 * time.Second):
		}
	}
}
