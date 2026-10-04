package apikey

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// Repository 秘钥仓储端口。
type Repository interface {
	// Save 新建或更新秘钥。
	Save(ctx context.Context, k *APIKey) error
	// FindByHash 按明文哈希查询（鉴权路径），不存在返回 ErrAPIKeyNotFound。
	FindByHash(ctx context.Context, keyHash string) (*APIKey, error)
	// FindByID 按 ID 查询，不存在返回 ErrAPIKeyNotFound。
	FindByID(ctx context.Context, id ID) (*APIKey, error)
	// ListByUser 列出某用户的全部秘钥（按创建时间升序）。
	ListByUser(ctx context.Context, ownerID user.ID) ([]*APIKey, error)
	// ListAll 列出全部秘钥（管理员视角）。
	ListAll(ctx context.Context) ([]*APIKey, error)
	// Delete 删除秘钥（硬删除，立即失效）。
	Delete(ctx context.Context, id ID) error
}
