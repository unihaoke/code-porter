package bot

import "context"

// BotRepository 机器人仓储端口。
type BotRepository interface {
	// Save 保存（新增或更新）。
	Save(ctx context.Context, b *Bot) error
	// Find 按 ID 查询，不存在返回 ErrBotNotFound。
	Find(ctx context.Context, id ID) (*Bot, error)
	// FindAll 返回全部机器人，按创建时间升序。
	FindAll(ctx context.Context) ([]*Bot, error)
	// FindByChannel 按渠道筛选。
	FindByChannel(ctx context.Context, ch Channel) ([]*Bot, error)
	// Delete 删除。
	Delete(ctx context.Context, id ID) error
}
