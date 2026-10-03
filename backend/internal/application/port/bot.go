package port

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/bot"
)

// BotSender 机器人出站消息端口：把任务结果推送回 IM 群。
//
// 实现放在基础设施层（infrastructure/bot），按渠道渲染不同格式：
// 飞书用富文本卡片，企业微信用 Markdown。
type BotSender interface {
	// Send 推送消息；返回错误仅用于日志，IM 平台偶发失败不应影响任务链路。
	Send(ctx context.Context, b *bot.Bot, msg bot.OutboundMessage) error
	// Supports 该发送器是否支持指定渠道。
	Supports(ch bot.Channel) bool
}
