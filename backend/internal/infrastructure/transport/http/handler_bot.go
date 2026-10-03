package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// BotHandlers 机器人配置管理接口（网页管理端调用）。
type BotHandlers struct {
	admin      *gateway.BotAdminUseCase
	publicAddr string
	log        port.Logger
}

// NewBotHandlers 构造处理器。
func NewBotHandlers(admin *gateway.BotAdminUseCase, publicAddr string, log port.Logger) *BotHandlers {
	return &BotHandlers{
		admin:      admin,
		publicAddr: strings.TrimRight(publicAddr, "/"),
		log:        log.With(port.F("h", "bots")),
	}
}

// botSpecDTO 机器人配置请求体。
type botSpecDTO struct {
	Name         string `json:"name"`
	Channel      string `json:"channel"`
	Model        string `json:"model,omitempty"`
	Mode         string `json:"mode,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	WebhookURL   string `json:"webhook_url,omitempty"`
	Secret       string `json:"secret,omitempty"`
	Token        string `json:"token,omitempty"`
	AESKey       string `json:"aes_key,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	MentionOnly  *bool  `json:"mention_only,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

// toSpec 转换为领域输入（零值字段保持「不修改」语义）。
func (d botSpecDTO) toSpec() (bot.Spec, error) {
	spec := bot.Spec{
		Name:         d.Name,
		AgentID:      d.AgentID,
		WebhookURL:   d.WebhookURL,
		Secret:       d.Secret,
		Token:        d.Token,
		AESKey:       d.AESKey,
		SystemPrompt: d.SystemPrompt,
	}
	if d.Channel != "" {
		ch, err := bot.ParseChannel(d.Channel)
		if err != nil {
			return spec, err
		}
		spec.Channel = ch
	}
	if d.Model != "" {
		m, err := model.Parse(d.Model)
		if err != nil {
			return spec, err
		}
		spec.Model = m
	}
	if d.Mode != "" {
		spec.Mode = task.ParseMode(d.Mode)
	}
	if d.MentionOnly != nil {
		spec.MentionOnly = *d.MentionOnly
	}
	if d.Enabled != nil {
		spec.Enabled = *d.Enabled
	}
	return spec, nil
}

// botView 机器人对外视图：隐藏密钥，补充回调地址与可用性。
func (h *BotHandlers) view(b *bot.Bot) map[string]any {
	callback := ""
	if h.publicAddr != "" {
		callback = h.publicAddr + "/webhook/" + b.Channel().String() + "/" + string(b.ID())
	}
	return map[string]any{
		"id":            string(b.ID()),
		"name":          b.Name(),
		"channel":       b.Channel().String(),
		"channel_name":  b.Channel().DisplayName(),
		"enabled":       b.Enabled(),
		"model":         b.Model().String(),
		"mode":          b.Mode().String(),
		"agent_id":      b.AgentID(),
		"webhook_url":   maskSecret(b.WebhookURL()),
		"has_secret":    b.Secret() != "",
		"has_token":     b.Token() != "",
		"has_aes_key":   b.AESKey() != "",
		"system_prompt": b.SystemPrompt(),
		"mention_only":  b.MentionOnly(),
		"callback_url":  callback,
		"can_receive":   b.CanReceive(),
		"can_reply":     b.CanReply(),
		"created_at":    b.CreatedAt().Unix(),
		"updated_at":    b.UpdatedAt().Unix(),
	}
}

// maskSecret 遮蔽 Webhook 中的 key 参数，避免管理端页面泄露凭据。
func maskSecret(url string) string {
	idx := strings.LastIndex(url, "=")
	if idx <= 0 || idx+1 >= len(url) {
		return url
	}
	prefix := url[:idx+1]
	tail := url[idx+1:]
	if len(tail) <= 4 {
		return prefix + "****"
	}
	return prefix + tail[:4] + "****" + tail[len(tail)-2:]
}

// List 列出全部机器人。
func (h *BotHandlers) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.admin.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, b := range list {
		out = append(out, h.view(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": out})
}

// Create 创建机器人。
func (h *BotHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var dto botSpecDTO
	if err := decodeBody(r, &dto); err != nil {
		writeErr(w, err)
		return
	}
	spec, err := dto.toSpec()
	if err != nil {
		writeErr(w, err)
		return
	}
	if dto.Enabled == nil {
		spec.Enabled = true
	}
	b, err := h.admin.Create(r.Context(), gateway.CreateCommand{Spec: spec})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"bot": h.view(b)})
}

// Get 查询单个机器人。
func (h *BotHandlers) Get(w http.ResponseWriter, r *http.Request) {
	b, err := h.admin.Get(r.Context(), bot.ID(r.PathValue("id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": h.view(b)})
}

// Update 更新机器人（局部更新）。
func (h *BotHandlers) Update(w http.ResponseWriter, r *http.Request) {
	var dto botSpecDTO
	if err := decodeBody(r, &dto); err != nil {
		writeErr(w, err)
		return
	}
	spec, err := dto.toSpec()
	if err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.admin.Update(r.Context(), gateway.UpdateCommand{ID: bot.ID(r.PathValue("id")), Spec: spec})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": h.view(b)})
}

// Delete 删除机器人。
func (h *BotHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.admin.Delete(r.Context(), bot.ID(r.PathValue("id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// Toggle 启停机器人。
func (h *BotHandlers) Toggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.admin.SetEnabled(r.Context(), bot.ID(r.PathValue("id")), req.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": h.view(b)})
}

// decodeBody 读取并解析 JSON 请求体（带大小上限）。
func decodeBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}
