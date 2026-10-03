package bot

import (
	"strings"
	"time"
)

// Format 出站消息格式，由基础设施层按渠道能力渲染。
type Format string

const (
	// FormatText 纯文本（两个渠道都支持）。
	FormatText Format = "text"
	// FormatMarkdown Markdown（企微支持 markdown，飞书支持富文本）。
	FormatMarkdown Format = "markdown"
	// FormatCard 交互卡片（飞书 interactive card / 企微模板卡片）。
	FormatCard Format = "card"
)

// String 返回格式名。
func (f Format) String() string { return string(f) }

// ChatType 会话类型。
type ChatType string

const (
	// ChatGroup 群聊。
	ChatGroup ChatType = "group"
	// ChatP2P 单聊。
	ChatP2P ChatType = "p2p"
)

// String 返回会话类型。
func (c ChatType) String() string { return string(c) }

// InboundMessage 入站消息：由 IM 平台回调翻译而来的标准化消息。
type InboundMessage struct {
	// BotID 命中的机器人。
	BotID ID
	// Channel 来源渠道。
	Channel Channel
	// EventID 事件 ID，用于幂等去重（IM 平台会重试回调）。
	EventID string
	// ChatID 群或会话 ID，回复时用于定位。
	ChatID string
	// ChatType 会话类型。
	ChatType ChatType
	// SenderID 发送者 ID。
	SenderID string
	// Text 去掉 @提及后的纯文本。
	Text string
	// RawText 原始文本。
	RawText string
	// Mentioned 该消息是否 @了机器人。
	Mentioned bool
	// ReceivedAt 接收时间。
	ReceivedAt time.Time
}

// IsEmpty 是否为空消息（无有效文本）。
func (m InboundMessage) IsEmpty() bool { return strings.TrimSpace(m.Text) == "" }

// OutboundMessage 出站消息：任务结果回推到 IM 群。
type OutboundMessage struct {
	// BotID 来源机器人。
	BotID ID
	// Channel 目标渠道。
	Channel Channel
	// ChatID 目标会话。
	ChatID string
	// Format 期望的渲染格式。
	Format Format
	// Content 正文。
	Content string
	// Title 卡片标题，非卡片格式可留空。
	Title string
	// ReplyTo 被回复的消息 ID（部分渠道支持线程回复）。
	ReplyTo string
	// Error 若非空表示这是一条失败通知。
	Error string
}
