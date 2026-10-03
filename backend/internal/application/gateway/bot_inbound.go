package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// maxReplyRunes IM 单条消息的保守长度上限。
//
// 企业微信 markdown 消息上限 4096 字节，中文按 UTF-8 约 3 字节/字，
// 这里统一按字符数截断并追加省略说明，避免超限被平台拒绝。
const maxReplyRunes = 1200

// BotInboundUseCase 机器人入站消息用例：IM 群消息 → CodePorter 任务 → 结果回推群。
//
// IM 平台对回调有严格超时（飞书 3s、企业微信 5s），而本地 AI 任务往往耗时数十秒，
// 因此这里采用「立即受理 + 异步回推」：提交任务后马上返回，
// 由后台协程等待任务终止事件再用 Webhook 推送结果。
type BotInboundUseCase struct {
	botRepo      bot.BotRepository
	submit       *SubmitTaskUseCase
	sender       port.BotSender
	defaultModel model.Model
	clock        port.Clock
	log          port.Logger
	policy       TaskPolicy
	ackTemplate  string
	replyTimeout time.Duration
}

// NewBotInboundUseCase 构造用例。
func NewBotInboundUseCase(
	botRepo bot.BotRepository,
	submit *SubmitTaskUseCase,
	sender port.BotSender,
	defaultModel model.Model,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *BotInboundUseCase {
	policy = policy.withDefaults()
	return &BotInboundUseCase{
		botRepo:      botRepo,
		submit:       submit,
		sender:       sender,
		defaultModel: defaultModel,
		clock:        clock,
		log:          log.With(port.F("uc", "bot_inbound")),
		policy:       policy,
		ackTemplate:  "已收到，正在用 %s 处理中…（任务 %s）",
		replyTimeout: policy.TTL,
	}
}

// BotInboundResult 入站消息处理结果。
type BotInboundResult struct {
	// BotID 命中的机器人。
	BotID string `json:"bot_id"`
	// TaskID 生成的任务 ID。
	TaskID string `json:"task_id"`
	// Accepted 是否已受理（进入任务链路）。
	Accepted bool `json:"accepted"`
	// Ignored 是否被忽略（未启用 / 未 @ / 空消息）。
	Ignored bool `json:"ignored"`
	// Reason 忽略或失败原因。
	Reason string `json:"reason,omitempty"`
}

// Handle 处理一条入站消息。
//
// 返回的 error 表示「受理失败」（机器人不存在、提交任务失败等），
// 调用方应据此返回 4xx/5xx；被忽略的消息不返回 error，仅置 Ignored。
func (u *BotInboundUseCase) Handle(ctx context.Context, msg bot.InboundMessage) (*BotInboundResult, error) {
	b, err := u.botRepo.Find(ctx, msg.BotID)
	if err != nil {
		return nil, err
	}
	res := &BotInboundResult{BotID: string(b.ID())}

	switch {
	case !b.CanReceive():
		res.Ignored = true
		res.Reason = "bot is disabled or missing callback credentials"
		return res, nil
	case msg.IsEmpty():
		res.Ignored = true
		res.Reason = "empty message"
		return res, nil
	case b.MentionOnly() && msg.ChatType == bot.ChatGroup && !msg.Mentioned:
		res.Ignored = true
		res.Reason = "not mentioned in group"
		return res, nil
	}

	m := b.Model()
	if m == "" {
		m = u.defaultModel
	}
	if m == "" {
		m = model.ClaudeCode
	}

	submitted, err := u.submit.Execute(ctx, SubmitTaskCommand{
		APIKeyID: "bot:" + string(b.ID()),
		AgentID:  agentIDOrEmpty(b.AgentID()),
		Model:    m,
		Prompt:   b.PromptFor(msg.Text),
		Stream:   false,
		Mode:     modeOrDefault(b.Mode()),
	})
	if err != nil {
		return nil, err
	}

	res.Accepted = true
	res.TaskID = submitted.TaskID

	// 异步等待结果并回推：脱离请求上下文，避免回调返回后协程被取消。
	go u.waitAndReply(b, msg, submitted)

	u.log.Info("bot message accepted",
		port.F("bot_id", string(b.ID())),
		port.F("channel", b.Channel().String()),
		port.F("task_id", submitted.TaskID),
		port.F("chat", msg.ChatID))
	return res, nil
}

// waitAndReply 等待任务终止事件并把结果推送回 IM 群。
func (u *BotInboundUseCase) waitAndReply(b *bot.Bot, msg bot.InboundMessage, submitted *SubmitTaskResult) {
	defer submitted.Close()

	bg, cancel := context.WithTimeout(context.Background(), u.replyTimeout+30*time.Second)
	defer cancel()

	out := bot.OutboundMessage{
		BotID:   b.ID(),
		Channel: b.Channel(),
		ChatID:  msg.ChatID,
		ReplyTo: msg.EventID,
	}
	if b.Channel() == bot.ChannelWecom {
		out.Format = bot.FormatMarkdown
	} else {
		out.Format = bot.FormatCard
	}

	var sb strings.Builder
	finished := false
	for !finished {
		select {
		case <-bg.Done():
			out.Error = "任务超时，未拿到本地 AI 的返回结果"
			out.Content = "⏱ " + out.Error
			finished = true
		case ev, ok := <-submitted.Events:
			if !ok {
				out.Error = "任务事件流已关闭"
				out.Content = "⚠️ " + out.Error
				finished = true
				continue
			}
			switch ev.Type {
			case port.EventChunk:
				sb.WriteString(ev.Content)
			case port.EventDone:
				// Done 事件可能只带最终结果片段，也可能只带终止信号。
				if ev.Content != "" && sb.Len() == 0 {
					sb.WriteString(ev.Content)
				}
				content := strings.TrimSpace(sb.String())
				if content == "" {
					content = "（本地 AI 未返回内容）"
				}
				out.Title = "CodePorter · " + b.Name()
				out.Content = truncateRunes(content, maxReplyRunes)
				finished = true
			case port.EventError, port.EventRejected:
				out.Error = ev.Message
				out.Content = "❌ " + ev.Message
				finished = true
			}
		}
	}

	if !b.CanReply() {
		u.log.Warn("bot cannot reply, webhook missing",
			port.F("bot_id", string(b.ID())),
			port.F("task_id", submitted.TaskID))
		return
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sendCancel()
	if err := u.sender.Send(sendCtx, b, out); err != nil {
		u.log.Error("reply to im failed",
			port.F("bot_id", string(b.ID())),
			port.F("channel", b.Channel().String()),
			port.F("err", err.Error()))
		return
	}
	u.log.Info("bot replied",
		port.F("bot_id", string(b.ID())),
		port.F("task_id", submitted.TaskID),
		port.F("chars", len(out.Content)))
}

// truncateRunes 按字符数截断并追加省略提示。
func truncateRunes(s string, limit int) string {
	if limit <= 0 || len([]rune(s)) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "\n\n…（内容过长已截断）"
}

// agentIDOrEmpty 把字符串转为 Agent ID（空串保持空，交由 registry 解析默认 Agent）。
func agentIDOrEmpty(s string) agent.ID { return agent.ID(s) }

// modeOrDefault 空模式回退为队列模式。
func modeOrDefault(m task.DeliveryMode) task.DeliveryMode {
	if m == "" {
		return task.ModePull
	}
	return m
}
