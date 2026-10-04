package user

import "context"

// Repository 用户仓储端口。
type Repository interface {
	// Save 新建或更新用户。
	Save(ctx context.Context, u *User) error
	// FindByID 按 ID 查询，不存在返回 ErrUserNotFound。
	FindByID(ctx context.Context, id ID) (*User, error)
	// FindByUsername 按用户名查询（调用方应先 NormalizeUsername），不存在返回 ErrUserNotFound。
	FindByUsername(ctx context.Context, username string) (*User, error)
	// List 列出全部用户（按创建时间升序）。
	List(ctx context.Context) ([]*User, error)
	// Count 返回用户总数（用于判断是否需要种子化/迁移）。
	Count(ctx context.Context) (int64, error)
	// Delete 删除用户；关联数据由存储层外键级联清理。
	Delete(ctx context.Context, id ID) error
}
