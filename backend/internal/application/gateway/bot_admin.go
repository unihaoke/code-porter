package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/bot"
)

// BotAdminUseCase 机器人配置管理用例：网页管理端通过它维护飞书 / 企业微信机器人。
type BotAdminUseCase struct {
	repo  bot.BotRepository
	clock port.Clock
	log   port.Logger
}

// NewBotAdminUseCase 构造用例。
func NewBotAdminUseCase(repo bot.BotRepository, clock port.Clock, log port.Logger) *BotAdminUseCase {
	return &BotAdminUseCase{
		repo:  repo,
		clock: clock,
		log:   log.With(port.F("uc", "bot_admin")),
	}
}

// CreateCommand 创建机器人命令。
type CreateCommand struct {
	Spec bot.Spec
}

// UpdateCommand 更新机器人命令。
type UpdateCommand struct {
	ID   bot.ID
	Spec bot.Spec
}

// Create 创建机器人。
func (u *BotAdminUseCase) Create(ctx context.Context, cmd CreateCommand) (*bot.Bot, error) {
	spec := cmd.Spec
	spec.Now = u.clock.Now()
	b, err := bot.NewBot(spec)
	if err != nil {
		return nil, err
	}
	if err := u.repo.Save(ctx, b); err != nil {
		return nil, err
	}
	u.log.Info("bot created",
		port.F("bot_id", string(b.ID())),
		port.F("channel", b.Channel().String()),
		port.F("name", b.Name()))
	return b, nil
}

// Update 更新机器人（局部更新：空字段保持不变）。
func (u *BotAdminUseCase) Update(ctx context.Context, cmd UpdateCommand) (*bot.Bot, error) {
	b, err := u.repo.Find(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	cp := b.Clone()
	spec := cmd.Spec
	spec.Now = u.clock.Now()
	if err := cp.Update(spec, spec.Now); err != nil {
		return nil, err
	}
	if err := u.repo.Save(ctx, cp); err != nil {
		return nil, err
	}
	u.log.Info("bot updated", port.F("bot_id", string(cp.ID())))
	return cp, nil
}

// SetEnabled 启停机器人。
func (u *BotAdminUseCase) SetEnabled(ctx context.Context, id bot.ID, enabled bool) (*bot.Bot, error) {
	b, err := u.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	cp := b.Clone()
	now := u.clock.Now()
	if enabled {
		cp.Enable(now)
	} else {
		cp.Disable(now)
	}
	if err := u.repo.Save(ctx, cp); err != nil {
		return nil, err
	}
	u.log.Info("bot toggled", port.F("bot_id", string(id)), port.F("enabled", enabled))
	return cp, nil
}

// Delete 删除机器人。
func (u *BotAdminUseCase) Delete(ctx context.Context, id bot.ID) error {
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	u.log.Info("bot deleted", port.F("bot_id", string(id)))
	return nil
}

// Get 查询单个机器人。
func (u *BotAdminUseCase) Get(ctx context.Context, id bot.ID) (*bot.Bot, error) {
	return u.repo.Find(ctx, id)
}

// List 列出全部机器人。
func (u *BotAdminUseCase) List(ctx context.Context) ([]*bot.Bot, error) {
	return u.repo.FindAll(ctx)
}
