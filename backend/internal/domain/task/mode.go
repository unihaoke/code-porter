package task

import "strings"

// DeliveryMode 任务投递通路，对应 PRD 的双通路架构。
type DeliveryMode string

const (
	// ModePull 队列模式（默认）：任务进入 Agent 私有队列，由 LocalAgent 主动拉取。
	ModePull DeliveryMode = "pull"
	// ModeDirect SSE 直连模式：通过预建立的 WebSocket 长连接实时推送，不进队列。
	ModeDirect DeliveryMode = "direct"
)

// ParseMode 解析投递模式，空值回退为 ModePull。
func ParseMode(s string) DeliveryMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "direct", "sse", "ws":
		return ModeDirect
	case "pull", "queue", "":
		return ModePull
	}
	return ModePull
}

// String 返回模式名。
func (m DeliveryMode) String() string { return string(m) }
