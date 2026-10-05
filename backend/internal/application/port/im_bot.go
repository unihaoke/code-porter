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

// IMCardPhase 流式卡片的生命周期阶段，决定头部颜色与 streaming_mode。
type IMCardPhase string

const (
	// IMCardRunning 处理中：卡片处于流式更新模式（平台对增量文本做打字机渲染）。
	IMCardRunning IMCardPhase = "running"
	// IMCardDone 完成：卡片定型，退出流式模式。
	IMCardDone IMCardPhase = "done"
	// IMCardFailed 失败：卡片以错误态定型。
	IMCardFailed IMCardPhase = "failed"
)

// IMCardState 流式卡片的平台无关渲染状态。
//
// 一条 IM 消息对应一张卡片：处理中反复整体更新（平台按元素 diff 出打字机效果），
// 终态做最后一次定型更新。过程（思考/工具）与正文分两个区域渲染。
type IMCardState struct {
	// Phase 生命周期阶段。
	Phase IMCardPhase
	// Title 卡片头部标题。
	Title string
	// Summary 聊天栏消息预览（如「思考中」「正在输出」「已完成」）。
	Summary string
	// Process 思考与工具调用过程（markdown），渲染为可折叠面板；为空则不显示。
	Process string
	// ProcessActive 过程是否仍在进行：true 时面板展开并显示「思考中」，false 时折叠。
	ProcessActive bool
	// Body 最终正文（markdown），流式阶段以打字机效果增长。
	Body string
	// Footer 底部状态行（如「🧠 正在思考」「✍️ 正在输出」）；终态留空。
	Footer string
}

// IMBotRunner 本地 IM 机器人运行时端口（基础设施层按平台实现，如飞书长连接）。
//
// 与网关无关：机器人运行在 Agent 进程内，消息直接本地处理。
type IMBotRunner interface {
	// Start 建立连接并阻塞运行，直到 ctx 取消；SDK 内部负责心跳与自动重连。
	Start(ctx context.Context) error
	// SendText 向会话发送纯文本（用于「已收到」等短通知）。
	SendText(ctx context.Context, chatID, text string) error
	// SendCard 向会话发送一次性卡片/markdown 正文（不做后续更新，用于兜底通知）。
	SendCard(ctx context.Context, chatID, title, markdown string) error
	// OpenStreamCard 创建一张流式卡片并返回平台消息 ID，后续用 UpdateStreamCard 更新。
	OpenStreamCard(ctx context.Context, chatID string, state IMCardState) (messageID string, err error)
	// UpdateStreamCard 用完整状态整体更新卡片；调用方负责节流与串行化。
	UpdateStreamCard(ctx context.Context, messageID string, state IMCardState) error
}
