package bot

import (
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// ID 机器人唯一标识，同时出现在回调 URL 中（/webhook/{channel}/{id}）。
type ID string

// String 返回字符串形式。
func (i ID) String() string { return string(i) }

// NewID 生成机器人 ID。
func NewID() ID { return ID(id.New("bot_")) }

// ErrBotNotFound 机器人不存在。
var ErrBotNotFound = apperr.New(apperr.CodeNotFound, "bot not found")

// ErrOwnerRequired 机器人必须归属某个用户。
var ErrOwnerRequired = apperr.New(apperr.CodeInvalidParam, "bot owner is required")

// Spec 创建 / 更新机器人的输入。
type Spec struct {
	// OwnerID 归属用户（租户）。
	OwnerID user.ID
	Name    string
	// Channel IM 渠道。
	Channel Channel
	// Model 该机器人默认使用的本地 AI 工具，为空时取全局默认。
	Model model.Model
	// Mode 投递通路偏好（群聊场景建议 pull，避免长任务拖住连接）。
	Mode task.DeliveryMode
	// AgentID 固定路由的 Agent，为空则由默认 Agent 承接。
	AgentID string
	// WebhookURL 渠道提供的群机器人 Webhook，用于出站推送结果。
	WebhookURL string
	// Secret 飞书签名校验密钥 / 企业微信可留空。
	Secret string
	// Token 回调校验 Token：飞书为 Verification Token，企业微信为回调 Token。
	Token string
	// AESKey 回调消息加密密钥（43 位 Base64），未开启加密时可留空。
	AESKey string
	// SystemPrompt 附加在该机器人所有任务前的系统提示。
	SystemPrompt string
	// MentionOnly 群聊中仅响应 @机器人 的消息。
	MentionOnly bool
	// Enabled 是否启用。
	Enabled bool
	// Now 时间戳。
	Now time.Time
}

// Bot 机器人聚合根。
type Bot struct {
	id           ID
	ownerID      user.ID
	name         string
	channel      Channel
	enabled      bool
	model        model.Model
	mode         task.DeliveryMode
	agentID      string
	webhookURL   string
	secret       string
	token        string
	aesKey       string
	systemPrompt string
	mentionOnly  bool
	createdAt    time.Time
	updatedAt    time.Time
}

// NewBot 创建机器人并校验必填项。
func NewBot(spec Spec) (*Bot, error) {
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "bot name is required")
	}
	if spec.Channel == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "bot channel is required")
	}
	if spec.OwnerID == "" {
		return nil, ErrOwnerRequired
	}
	b := &Bot{
		id:        NewID(),
		ownerID:   spec.OwnerID,
		createdAt: now,
		updatedAt: now,
	}
	if err := b.apply(spec, now); err != nil {
		return nil, err
	}
	return b, nil
}

// apply 校验并写入可变字段。
func (b *Bot) apply(spec Spec, now time.Time) error {
	if spec.Name != "" {
		b.name = strings.TrimSpace(spec.Name)
	}
	if spec.Channel != "" {
		b.channel = spec.Channel
	}
	if spec.WebhookURL != "" {
		url := strings.TrimSpace(spec.WebhookURL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return apperr.New(apperr.CodeInvalidParam, "webhook url must start with http:// or https://")
		}
		b.webhookURL = url
	}
	if spec.AESKey != "" {
		key := strings.TrimSpace(spec.AESKey)
		if len(key) != 43 {
			return apperr.New(apperr.CodeInvalidParam,
				"encoding aes key must be 43 chars (base64 of 32 bytes)")
		}
		b.aesKey = key
	}
	b.model = spec.Model
	b.mode = spec.Mode
	b.agentID = strings.TrimSpace(spec.AgentID)
	b.secret = strings.TrimSpace(spec.Secret)
	b.token = strings.TrimSpace(spec.Token)
	b.systemPrompt = spec.SystemPrompt
	b.mentionOnly = spec.MentionOnly
	b.enabled = spec.Enabled
	b.updatedAt = now
	return nil
}

// Update 更新机器人配置。
func (b *Bot) Update(spec Spec, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	return b.apply(spec, now)
}

// Clone 返回副本（仓储以「取副本 → 修改 → 回写」方式避免数据竞争）。
func (b *Bot) Clone() *Bot {
	cp := *b
	return &cp
}

// --- 只读访问器 ---

// ID 标识。
func (b *Bot) ID() ID { return b.id }

// OwnerID 归属用户（租户）。
func (b *Bot) OwnerID() user.ID { return b.ownerID }

// Name 名称。
func (b *Bot) Name() string { return b.name }

// Channel 渠道。
func (b *Bot) Channel() Channel { return b.channel }

// Enabled 是否启用。
func (b *Bot) Enabled() bool { return b.enabled }

// Model 默认模型。
func (b *Bot) Model() model.Model { return b.model }

// Mode 投递通路偏好。
func (b *Bot) Mode() task.DeliveryMode { return b.mode }

// AgentID 固定路由的 Agent。
func (b *Bot) AgentID() string { return b.agentID }

// WebhookURL 出站 Webhook 地址。
func (b *Bot) WebhookURL() string { return b.webhookURL }

// Secret 签名密钥。
func (b *Bot) Secret() string { return b.secret }

// Token 回调校验 Token。
func (b *Bot) Token() string { return b.token }

// AESKey 回调消息加密密钥。
func (b *Bot) AESKey() string { return b.aesKey }

// SystemPrompt 附加系统提示。
func (b *Bot) SystemPrompt() string { return b.systemPrompt }

// MentionOnly 是否仅响应 @消息。
func (b *Bot) MentionOnly() bool { return b.mentionOnly }

// CreatedAt 创建时间。
func (b *Bot) CreatedAt() time.Time { return b.createdAt }

// UpdatedAt 更新时间。
func (b *Bot) UpdatedAt() time.Time { return b.updatedAt }

// --- 行为 ---

// Enable 启用机器人。
func (b *Bot) Enable(now time.Time) { b.enabled = true; b.updatedAt = now }

// Disable 停用机器人。
func (b *Bot) Disable(now time.Time) { b.enabled = false; b.updatedAt = now }

// CanReceive 是否能接收回调：启用且（未开启加密时）凭据完整。
//
// 企业微信的回调强制加密，缺少 AESKey 时无法解析消息，直接判定不可用。
func (b *Bot) CanReceive() bool {
	if !b.enabled {
		return false
	}
	if b.channel == ChannelWecom && b.aesKey == "" {
		return false
	}
	return true
}

// CanReply 是否能向群里推送结果：启用且配置了出站 Webhook。
func (b *Bot) CanReply() bool { return b.enabled && b.webhookURL != "" }

// Rewrite 回填持久化恢复出来的 ID、归属与时间戳。
//
// 仅供仓储反序列化使用：领域构造函数总是生成新 ID，而加载历史数据必须保留原 ID。
func (b *Bot) Rewrite(id ID, ownerID user.ID, createdAt, updatedAt time.Time) {
	if id != "" {
		b.id = id
	}
	if ownerID != "" {
		b.ownerID = ownerID
	}
	if !createdAt.IsZero() {
		b.createdAt = createdAt
	}
	if !updatedAt.IsZero() {
		b.updatedAt = updatedAt
	}
}

// PromptFor 生成该机器人实际下发给本地 AI 的提示词（拼上系统提示）。
func (b *Bot) PromptFor(text string) string {
	if strings.TrimSpace(b.systemPrompt) == "" {
		return text
	}
	return b.systemPrompt + "\n\n" + text
}
