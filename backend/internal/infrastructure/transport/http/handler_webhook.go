package http

import (
	"io"
	"net/http"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/bot"
	infrabot "github.com/codeporter/code-porter/internal/infrastructure/bot"
)

// WebhookHandlers IM 平台回调入口。
//
// 路由形态：/webhook/{channel}/{bot_id}，bot_id 同时用于定位机器人配置，
// 这样一个网关可以同时服务多个飞书 / 企业微信机器人。
type WebhookHandlers struct {
	repo    bot.BotRepository
	inbound *gateway.BotInboundUseCase
	log     port.Logger
}

// NewWebhookHandlers 构造处理器。
func NewWebhookHandlers(repo bot.BotRepository, inbound *gateway.BotInboundUseCase, log port.Logger) *WebhookHandlers {
	return &WebhookHandlers{repo: repo, inbound: inbound, log: log.With(port.F("h", "webhook"))}
}

// resolve 按路径参数定位机器人。
func (h *WebhookHandlers) resolve(r *http.Request) (*bot.Bot, error) {
	return h.repo.Find(r.Context(), bot.ID(r.PathValue("id")))
}

// Feishu 飞书事件回调。
func (h *WebhookHandlers) Feishu(w http.ResponseWriter, r *http.Request) {
	b, err := h.resolve(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, err)
		return
	}
	msg, challenge, err := infrabot.ParseFeishu(b, body)
	if err != nil {
		h.log.Warn("feishu callback rejected", port.F("bot_id", string(b.ID())), port.F("err", err.Error()))
		writeErr(w, err)
		return
	}
	// URL 验证：飞书保存回调地址时会发一次 challenge，必须原样回显。
	if challenge != "" {
		writeJSON(w, http.StatusOK, map[string]any{"challenge": challenge})
		return
	}
	if msg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
		return
	}
	res, err := h.inbound.Handle(r.Context(), *msg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"task_id":  res.TaskID,
		"accepted": res.Accepted,
		"ignored":  res.Ignored,
		"reason":   res.Reason,
	})
}

// Wecom 企业微信回调：GET 用于 URL 验证，POST 用于接收消息。
func (h *WebhookHandlers) Wecom(w http.ResponseWriter, r *http.Request) {
	b, err := h.resolve(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	signature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")

	if r.Method == http.MethodGet {
		echo, err := infrabot.VerifyWecomURL(b, signature, timestamp, nonce, q.Get("echostr"))
		if err != nil {
			h.log.Warn("wecom url verify failed", port.F("bot_id", string(b.ID())), port.F("err", err.Error()))
			writeErr(w, err)
			return
		}
		// 企微要求原样返回解密后的 echostr 明文，不能包 JSON。
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(echo))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, err)
		return
	}
	msg, err := infrabot.ParseWecom(b, body, signature, timestamp, nonce)
	if err != nil {
		h.log.Warn("wecom callback rejected", port.F("bot_id", string(b.ID())), port.F("err", err.Error()))
		writeErr(w, err)
		return
	}
	if msg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
		return
	}
	res, err := h.inbound.Handle(r.Context(), *msg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"task_id":  res.TaskID,
		"accepted": res.Accepted,
		"ignored":  res.Ignored,
		"reason":   res.Reason,
	})
}
