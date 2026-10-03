package agent

import (
	"context"
)

// Repository Agent 仓储端口。
type Repository interface {
	// Save 新建或更新 Agent。
	Save(ctx context.Context, a *Agent) error
	// Find 按 ID 查询，不存在返回 ErrAgentNotFound。
	Find(ctx context.Context, id ID) (*Agent, error)
	// FindByToken 按 Token 查询（Agent 鉴权路径）。
	FindByToken(ctx context.Context, token string) (*Agent, error)
	// List 列出全部 Agent。
	List(ctx context.Context) ([]*Agent, error)
	// Delete 删除 Agent。
	Delete(ctx context.Context, id ID) error
}
