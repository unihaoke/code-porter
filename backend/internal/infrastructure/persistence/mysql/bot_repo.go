package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// BotRepository 机器人仓储的 MySQL 实现。
type BotRepository struct {
	db *sql.DB
}

// NewBotRepository 构造仓储。
func NewBotRepository(db *sql.DB) *BotRepository { return &BotRepository{db: db} }

// Save 新建或更新机器人。
func (r *BotRepository) Save(ctx context.Context, b *bot.Bot) error {
	// 注意：文本列均为 NOT NULL（见 0001_init.sql），空值一律落空串而非 NULL；
	// 仅 last_used_at 这类真正可空的时间列使用 NullTime。
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO bots
		(id, user_id, name, channel, enabled, model, mode, agent_id, webhook_url,
		 secret, token, aes_key, system_prompt, mention_only, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			name=VALUES(name), channel=VALUES(channel), enabled=VALUES(enabled),
			model=VALUES(model), mode=VALUES(mode), agent_id=VALUES(agent_id),
			webhook_url=VALUES(webhook_url), secret=VALUES(secret), token=VALUES(token),
			aes_key=VALUES(aes_key), system_prompt=VALUES(system_prompt),
			mention_only=VALUES(mention_only), updated_at=VALUES(updated_at)`,
		string(b.ID()), string(b.OwnerID()), b.Name(), string(b.Channel()), b.Enabled(),
		b.Model().String(), b.Mode().String(), b.AgentID(), b.WebhookURL(),
		b.Secret(), b.Token(), b.AESKey(), b.SystemPrompt(), b.MentionOnly(),
		b.CreatedAt(), b.UpdatedAt())
	if err != nil {
		return err
	}
	return nil
}

// Find 按 ID 查询。
func (r *BotRepository) Find(ctx context.Context, id bot.ID) (*bot.Bot, error) {
	row := r.db.QueryRowContext(ctx, botSelect+` WHERE id = ?`, string(id))
	return scanBot(row)
}

// FindAll 全部机器人。
func (r *BotRepository) FindAll(ctx context.Context) ([]*bot.Bot, error) {
	return r.queryBots(ctx, botSelect+` ORDER BY created_at ASC`)
}

// FindByOwner 某用户的机器人。
func (r *BotRepository) FindByOwner(ctx context.Context, ownerID user.ID) ([]*bot.Bot, error) {
	return r.queryBots(ctx, botSelect+` WHERE user_id = ? ORDER BY created_at ASC`, string(ownerID))
}

// FindByChannel 按渠道筛选。
func (r *BotRepository) FindByChannel(ctx context.Context, ch bot.Channel) ([]*bot.Bot, error) {
	return r.queryBots(ctx, botSelect+` WHERE channel = ? ORDER BY created_at ASC`, string(ch))
}

// Delete 删除机器人。
func (r *BotRepository) Delete(ctx context.Context, id bot.ID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM bots WHERE id = ?`, string(id))
	return err
}

// Count 机器人总数（迁移器判断是否空表用）。
func (r *BotRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bots`).Scan(&n)
	return n, err
}

const botSelect = `
	SELECT id, user_id, name, channel, enabled, model, mode, agent_id,
		webhook_url, secret, token, aes_key, system_prompt, mention_only, created_at, updated_at
	FROM bots`

func (r *BotRepository) queryBots(ctx context.Context, query string, args ...any) ([]*bot.Bot, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*bot.Bot
	for rows.Next() {
		b, err := scanBot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBot(s rowScanner) (*bot.Bot, error) {
	var (
		id, uid, name, ch                               string
		enabled, mentionOnly                            bool
		modelStr, modeStr, agentID                      string
		webhookURL, secret, token, aesKey, systemPrompt string
		createdAt, updatedAt                            = time.Time{}, time.Time{}
	)
	if err := s.Scan(&id, &uid, &name, &ch, &enabled, &modelStr, &modeStr, &agentID,
		&webhookURL, &secret, &token, &aesKey, &systemPrompt, &mentionOnly,
		&createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, bot.ErrBotNotFound
		}
		return nil, err
	}
	b, err := bot.NewBot(bot.Spec{
		OwnerID:      user.ID(uid),
		Name:         name,
		Channel:      bot.Channel(ch),
		Model:        model.Model(modelStr),
		Mode:         task.DeliveryMode(modeStr),
		AgentID:      agentID,
		WebhookURL:   webhookURL,
		Secret:       secret,
		Token:        token,
		AESKey:       aesKey,
		SystemPrompt: systemPrompt,
		MentionOnly:  mentionOnly,
		Enabled:      enabled,
		Now:          updatedAt,
	})
	if err != nil {
		return nil, err
	}
	b.Rewrite(bot.ID(id), user.ID(uid), createdAt, updatedAt)
	return b, nil
}
