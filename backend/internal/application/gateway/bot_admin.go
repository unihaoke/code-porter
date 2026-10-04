package gateway

import (
	"context"
	"strings"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// agentOwnerChecker 校验实例归属（*AgentRegistry 天然满足）。
type agentOwnerChecker interface {
	ResolveForOwner(ctx context.Context, ownerID user.ID, instanceID agent.ID) (*agent.Agent, error)
}

// BotAdminUseCase 机器人配置管理用例（按租户隔离，admin 全局可见）。
type BotAdminUseCase struct {
	repo   bot.BotRepository
	agents agentOwnerChecker
	clock  port.Clock
	log    port.Logger
}

// NewBotAdminUseCase 构造用例。
func NewBotAdminUseCase(repo bot.BotRepository, agents agentOwnerChecker, clock port.Clock, log port.Logger) *BotAdminUseCase {
	return &BotAdminUseCase{
		repo:   repo,
		agents: agents,
		clock:  clock,
		log:    log.With(port.F("uc", "bot_admin")),
	}
}

// Create 创建机器人；归属强制为操作者本人，固定实例必须是本人的客户端。
func (u *BotAdminUseCase) Create(ctx context.Context, actor *user.User, spec bot.Spec) (*bot.Bot, error) {
	if actor == nil {
		return nil, apperr.ErrUnauthorized
	}
	spec.OwnerID = actor.ID()
	if err := u.validateAgent(ctx, actor, spec.AgentID); err != nil {
		return nil, err
	}
	spec.Now = u.clock.Now()
	b, err := bot.NewBot(spec)
	if err != nil {
		return nil, err
	}
	if err := u.repo.Save(ctx, b); err != nil {
		return nil, err
	}
	u.log.Info("bot created",
		port.F("bot_id", string(b.ID())), port.F("user_id", string(b.OwnerID())),
		port.F("channel", b.Channel().String()), port.F("name", b.Name()))
	return b, nil
}

// Update 更新机器人（局部更新：空字段保持不变）。
func (u *BotAdminUseCase) Update(ctx context.Context, actor *user.User, id bot.ID, spec bot.Spec) (*bot.Bot, error) {
	cp, err := u.loadOwned(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if err := u.validateAgent(ctx, actor, spec.AgentID); err != nil {
		return nil, err
	}
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
func (u *BotAdminUseCase) SetEnabled(ctx context.Context, actor *user.User, id bot.ID, enabled bool) (*bot.Bot, error) {
	cp, err := u.loadOwned(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	now := u.clock.Now()
	if enabled {
		cp.Enable(now)
	} else {
		cp.Disable(now)
	}
	if err := u.repo.Save(ctx, cp); err != nil {
		return nil, err
	}
	return cp, nil
}

// Delete 删除机器人。
func (u *BotAdminUseCase) Delete(ctx context.Context, actor *user.User, id bot.ID) error {
	if _, err := u.loadOwned(ctx, actor, id); err != nil {
		return err
	}
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	u.log.Info("bot deleted", port.F("bot_id", string(id)))
	return nil
}

// Get 查询单个机器人。
func (u *BotAdminUseCase) Get(ctx context.Context, actor *user.User, id bot.ID) (*bot.Bot, error) {
	return u.loadOwned(ctx, actor, id)
}

// List 列出机器人：普通用户只看自己的，admin 看全部。
func (u *BotAdminUseCase) List(ctx context.Context, actor *user.User) ([]*bot.Bot, error) {
	if actor == nil {
		return nil, apperr.ErrUnauthorized
	}
	if actor.IsAdmin() {
		return u.repo.FindAll(ctx)
	}
	return u.repo.FindByOwner(ctx, actor.ID())
}

// loadOwned 取出机器人并校验归属：非属主（且非 admin）一律 not found，
// 避免通过 ID 探测他人机器人是否存在。
func (u *BotAdminUseCase) loadOwned(ctx context.Context, actor *user.User, id bot.ID) (*bot.Bot, error) {
	if actor == nil {
		return nil, apperr.ErrUnauthorized
	}
	b, err := u.repo.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if !actor.IsAdmin() && b.OwnerID() != actor.ID() {
		return nil, bot.ErrBotNotFound
	}
	return b, nil
}

// validateAgent 校验机器人绑定的固定实例归属；空串表示走自动路由，不校验。
func (u *BotAdminUseCase) validateAgent(ctx context.Context, actor *user.User, fixedAgentID string) error {
	id := strings.TrimSpace(fixedAgentID)
	if id == "" || u.agents == nil {
		return nil
	}
	if _, err := u.agents.ResolveForOwner(ctx, actor.ID(), agent.ID(id)); err != nil {
		return apperr.Wrap(apperr.CodeInvalidParam, "agent_id 不属于当前用户或尚未注册", err)
	}
	return nil
}
