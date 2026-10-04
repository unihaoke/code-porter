package port

import "context"

// ChatType 本地 IM 机器人的会话类型。
type ChatType string

const (
	// ChatP2P 私聊。
	ChatP2P ChatType = "p2p"
	// ChatGroup 群聊。
	ChatGroup ChatType = "group"
)

// IMBotMessage 本地 IM 机器人收到的标准化入站消息（由基础设施层翻译）。
type IMBotMessage struct {
	// EventID 平台事件/消息 ID，用于幂等去重。
	EventID string
	// ChatID 会话 ID，回复时定位。
	ChatID string
	// ChatType p2p / group。
	ChatType ChatType
	// SenderID 发送者在应用内的 open_id。
	SenderID string
	// Text 去掉 @占位符后的纯文本。
	Text string
	// RawText 原始文本。
	RawText string
	// Mentioned 该消息是否 @了机器人。
	Mentioned bool
}

// IMBotRunner 本地 IM 机器人运行时端口（基础设施层按平台实现，如飞书长连接）。
//
// 与网关无关：机器人运行在 Agent 进程内，消息直接本地处理。
type IMBotRunner interface {
	// Start 建立连接并阻塞运行，直到 ctx 取消；SDK 内部负责心跳与自动重连。
	Start(ctx context.Context) error
	// SendText 向会话发送纯文本（用于「已收到」等短通知）。
	SendText(ctx context.Context, chatID, text string) error
	// SendCard 向会话发送卡片/markdown 正文（用于任务结果）。
	SendCard(ctx context.Context, chatID, title, markdown string) error
}
