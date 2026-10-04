package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// ChatHandler 网页对话入口：控制台页面通过 SSE 实时接收本地 AI 的输出。
type ChatHandler struct {
	chat   *gateway.ChatUseCase
	policy gateway.TaskPolicy
	log    port.Logger
}

// NewChatHandler 构造处理器。
func NewChatHandler(chat *gateway.ChatUseCase, policy gateway.TaskPolicy, log port.Logger) *ChatHandler {
	return &ChatHandler{chat: chat, policy: policy.WithDefaults(), log: log.With(port.F("h", "chat"))}
}

// chatRequest 网页对话请求体。
type chatRequest struct {
	Messages []task.Message `json:"messages"`
	Model    string         `json:"model,omitempty"`
	Mode     string         `json:"mode,omitempty"`
	AgentID  string         `json:"agent_id,omitempty"`
	WorkDir  string         `json:"work_dir,omitempty"`
	Stream   *bool          `json:"stream,omitempty"`
	// MaxTokens 最大输出长度。
	MaxTokens int `json:"max_tokens,omitempty"`
	// Permission 可选文件权限收紧（read/write）；缺省 all。
	// 网页入口无秘钥上限，允许用户在界面里主动选择「只读分析」。
	Permission string `json:"permission,omitempty"`
}

// chatMetaEvent 首个事件，携带任务元信息，便于前端展示「正在排队 / 直连中」。
type chatMetaEvent struct {
	TaskID string `json:"task_id"`
	Model  string `json:"model"`
	Mode   string `json:"mode"`
	Agent  string `json:"agent"`
}

// ServeHTTP 处理一次网页对话。
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if len(req.Messages) == 0 {
		writeErr(w, apperr.New(apperr.CodeInvalidParam, "messages is required"))
		return
	}
	stream := true
	if req.Stream != nil {
		stream = *req.Stream
	}
	m, err := model.Parse(req.Model)
	if err != nil && req.Model != "" {
		writeErr(w, err)
		return
	}
	perm, ok := task.ParsePermission(req.Permission)
	if !ok {
		writeErr(w, apperr.New(apperr.CodeInvalidParam,
			"invalid permission, allowed values: read | write | all"))
		return
	}

	res, err := h.chat.Execute(r.Context(), gateway.ChatCommand{
		OwnerID:    userFromContext(r.Context()).ID(),
		APIKeyID:   "web",
		AgentID:    agent.ID(req.AgentID),
		Model:      m,
		Messages:   req.Messages,
		Mode:       task.ParseMode(req.Mode),
		Stream:     stream,
		WorkDir:    req.WorkDir,
		MaxTokens:  req.MaxTokens,
		Permission: perm,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	defer res.Close()

	if !stream {
		h.waitJSON(w, r, res)
		return
	}
	h.streamSSE(w, r, res)
}

// waitJSON 非流式：等待终止事件后一次性返回。
func (h *ChatHandler) waitJSON(w http.ResponseWriter, r *http.Request, res *gateway.SubmitTaskResult) {
	deadline := time.NewTimer(h.policy.RequestTimeout)
	defer deadline.Stop()

	var sb strings.Builder
	finished := false
	for !finished {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			writeErr(w, apperr.New(apperr.CodeTimeout, "task did not finish within timeout"))
			return
		case ev, ok := <-res.Events:
			if !ok {
				writeErr(w, apperr.New(apperr.CodeTimeout, "task event stream closed"))
				return
			}
			switch ev.Type {
			case port.EventChunk:
				sb.WriteString(ev.Content)
			case port.EventDone:
				// Done 事件携带的是完整结果（可能包含只上报最终结果的场景），
				// 直接用它覆盖已累加的片段，否则内容会被重复拼接一遍。
				if ev.Content != "" {
					sb.Reset()
					sb.WriteString(ev.Content)
				}
				finished = true
			case port.EventError, port.EventRejected:
				writeErr(w, apperr.New(codeFromEvent(ev), ev.Message))
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": res.TaskID,
		"model":   res.Model.String(),
		"mode":    res.Mode.String(),
		"content": sb.String(),
	})
}

// streamSSE 流式：以 SSE 事件推送片段与终止结果。
func (h *ChatHandler) streamSSE(w http.ResponseWriter, r *http.Request, res *gateway.SubmitTaskResult) {
	sse, err := NewSSEWriter(w)
	if err != nil {
		writeErr(w, err)
		return
	}
	meta, _ := json.Marshal(chatMetaEvent{
		TaskID: res.TaskID,
		Model:  res.Model.String(),
		Mode:   res.Mode.String(),
	})
	if err := sse.WriteEvent("meta", meta); err != nil {
		return
	}

	idle := time.NewTimer(h.policy.StreamIdleTimeout)
	defer idle.Stop()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	var sb strings.Builder
	for {
		select {
		case <-r.Context().Done():
			return
		case <-idle.C:
			_ = sse.WriteEvent("error", []byte(`{"message":"stream idle timeout"}`))
			sse.Done()
			return
		case <-keepAlive.C:
			_ = sse.WriteComment("keepalive")
		case ev, ok := <-res.Events:
			if !ok {
				sse.Done()
				return
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(h.policy.StreamIdleTimeout)
			switch ev.Type {
			case port.EventChunk:
				sb.WriteString(ev.Content)
				payload, _ := json.Marshal(map[string]string{"content": ev.Content})
				if err := sse.WriteEvent("chunk", payload); err != nil {
					return
				}
			case port.EventDone:
				payload, _ := json.Marshal(map[string]string{"content": sb.String()})
				_ = sse.WriteEvent("done", payload)
				sse.Done()
				return
			case port.EventError, port.EventRejected:
				payload, _ := json.Marshal(map[string]string{"message": ev.Message})
				_ = sse.WriteEvent("error", payload)
				sse.Done()
				return
			}
		}
	}
}

// codeFromEvent 把任务事件携带的错误码映射为领域错误码。
func codeFromEvent(ev port.TaskEvent) apperr.Code {
	if c := apperr.Code(ev.Code); c != "" {
		return c
	}
	return apperr.CodeInternal
}
