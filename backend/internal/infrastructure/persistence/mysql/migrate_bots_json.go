package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// legacyBotDTO 旧版 bots.json 的记录形状（与 infrastructure/bot 的 JSON 一致）。
type legacyBotDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Channel      string    `json:"channel"`
	Enabled      bool      `json:"enabled"`
	Model        string    `json:"model,omitempty"`
	Mode         string    `json:"mode,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	WebhookURL   string    `json:"webhook_url,omitempty"`
	Secret       string    `json:"secret,omitempty"`
	Token        string    `json:"token,omitempty"`
	AESKey       string    `json:"aes_key,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	MentionOnly  bool      `json:"mention_only"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// BotImporter 迁移所需的最小仓储能力（MySQL BotRepository 天然满足；测试可用 fake）。
type BotImporter interface {
	FindAll(ctx context.Context) ([]*bot.Bot, error)
	Save(ctx context.Context, b *bot.Bot) error
}

// MigrateBotsJSON 一次性迁移：
// 当 bots 表为空且 path 指向的旧 JSON 文件存在时，把全部机器人导入到 owner 名下，
// 成功后将文件改名为 <path>.migrated（不删除，便于回滚）。
// 表非空或文件不存在均不做任何操作。脏数据（无法构造聚合的记录）跳过。
func MigrateBotsJSON(ctx context.Context, repo BotImporter, path string, owner user.ID) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read legacy bots file %s: %w", path, err)
	}
	existing, err := repo.FindAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("check bots before migration: %w", err)
	}
	if len(existing) > 0 {
		return 0, nil
	}
	var list []legacyBotDTO
	if err := json.Unmarshal(raw, &list); err != nil {
		return 0, fmt.Errorf("parse legacy bots file: %w", err)
	}
	imported := 0
	for _, d := range list {
		ch := d.Channel
		if ch == "" {
			ch = string(bot.ChannelFeishu)
		}
		updated := d.UpdatedAt
		if updated.IsZero() {
			updated = time.Now()
		}
		created := d.CreatedAt
		if created.IsZero() {
			created = updated
		}
		b, err := bot.NewBot(bot.Spec{
			OwnerID:      owner,
			Name:         d.Name,
			Channel:      bot.Channel(ch),
			Model:        model.Model(d.Model),
			Mode:         task.DeliveryMode(d.Mode),
			AgentID:      d.AgentID,
			WebhookURL:   d.WebhookURL,
			Secret:       d.Secret,
			Token:        d.Token,
			AESKey:       d.AESKey,
			SystemPrompt: d.SystemPrompt,
			MentionOnly:  d.MentionOnly,
			Enabled:      d.Enabled,
			Now:          updated,
		})
		if err != nil {
			continue // 脏数据不阻断整体迁移
		}
		b.Rewrite(bot.ID(d.ID), owner, created, updated)
		if err := repo.Save(ctx, b); err != nil {
			return imported, fmt.Errorf("save migrated bot %s: %w", d.ID, err)
		}
		imported++
	}
	if err := os.Rename(path, path+".migrated"); err != nil {
		return imported, fmt.Errorf("rename legacy bots file after migration: %w", err)
	}
	return imported, nil
}
