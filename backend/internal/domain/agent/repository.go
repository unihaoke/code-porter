package agent

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// Repository Agent 仓储端口。
type Repository interface {
	// Save 新建或更新 Agent。
	Save(ctx context.Context, a *Agent) error
	// Find 按 ID 查询，不存在返回 ErrAgentNotFound。
	Find(ctx context.Context, id ID) (*Agent, error)
	// FindByIdentity 按「归属用户 + 实例 ID」查询（鉴权路径）；
	// 实例不存在或不属于该用户均返回 ErrAgentNotFound（不泄露他人资源存在性）。
	FindByIdentity(ctx context.Context, ownerID user.ID, id ID) (*Agent, error)
	// ListByOwner 列出某用户的全部 Agent。
	ListByOwner(ctx context.Context, ownerID user.ID) ([]*Agent, error)
	// List 列出全部 Agent（管理员视角）。
	List(ctx context.Context) ([]*Agent, error)
	// Delete 删除 Agent。
	Delete(ctx context.Context, id ID) error
}
