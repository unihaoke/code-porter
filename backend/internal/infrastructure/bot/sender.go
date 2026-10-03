package bot

import (
	"context"
	"net/http"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	domainbot "github.com/codeporter/code-porter/internal/domain/bot"
)

// Sender 按渠道分发的机器人出站发送器，实现 port.BotSender。
type Sender struct {
	client *http.Client
	log    port.Logger
}

// NewSender 构造发送器。
func NewSender(timeout time.Duration, log port.Logger) *Sender {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Sender{
		client: &http.Client{Timeout: timeout},
		log:    log.With(port.F("cmp", "bot_sender")),
	}
}

// Supports 是否支持该渠道。
func (s *Sender) Supports(ch domainbot.Channel) bool {
	switch ch {
	case domainbot.ChannelFeishu, domainbot.ChannelWecom:
		return true
	}
	return false
}

// Send 推送消息到对应渠道。
func (s *Sender) Send(ctx context.Context, b *domainbot.Bot, msg domainbot.OutboundMessage) error {
	if !s.Supports(msg.Channel) {
		return errUnsupportedChannel(msg.Channel)
	}
	switch msg.Channel {
	case domainbot.ChannelFeishu:
		return sendFeishu(ctx, s.client, b, msg)
	case domainbot.ChannelWecom:
		return sendWecom(ctx, s.client, b, msg)
	}
	return errUnsupportedChannel(msg.Channel)
}

// errUnsupportedChannel 渠道不支持。
func errUnsupportedChannel(ch domainbot.Channel) error {
	return &unsupportedError{channel: ch}
}

// unsupportedError 渠道不支持错误。
type unsupportedError struct{ channel domainbot.Channel }

// Error 实现 error。
func (e *unsupportedError) Error() string { return "bot: unsupported channel " + string(e.channel) }
