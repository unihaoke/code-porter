package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/openai"
)

// ModeHeader 通路选择请求头（pull / direct）。
const ModeHeader = "X-CodePorter-Mode"

// OperationHeader 操作类型请求头。
const OperationHeader = "X-CodePorter-Operation"

// WorkDirHeader 本地工作目录请求头。
const WorkDirHeader = "X-CodePorter-WorkDir"

// AgentHeader 指定目标 Agent（可选，MVP 单 Agent 可不传）。
const AgentHeader = "X-CodePorter-Agent"

// PermissionHeader 单次请求的文件操作权限（可选）：read | write | all。
// 只能在秘钥授予的权限上限内收紧，不能提权；缺省取秘钥权限。
const PermissionHeader = "X-CodePorter-Permission"

// TaskIDHeader 响应中回传的任务 ID。
const TaskIDHeader = "X-CodePorter-Task-Id"

// maxBodyBytes 请求体上限（8MB），防止超大 payload 打爆内存。
const maxBodyBytes = 8 << 20

// ChatCompletionHandler 处理 POST /v1/chat/completions（OpenAI 兼容入口）。
type ChatCompletionHandler struct {
	submit  *gateway.SubmitTaskUseCase
	limiter KeyedLimiter
	log     port.Logger
	policy  gateway.TaskPolicy
}

// KeyedLimiter 单 API Key 限流能力。
type KeyedLimiter interface {
	Allow(key string) bool
}

// NewChatCompletionHandler 构造处理器。
func NewChatCompletionHandler(
	submit *gateway.SubmitTaskUseCase,
	limiter KeyedLimiter,
	log port.Logger,
	policy gateway.TaskPolicy,
) *ChatCompletionHandler {
	return &ChatCompletionHandler{submit: submit, limiter: limiter, log: log.With(port.F("h", "chat_completions")), policy: policy}
}

// ServeHTTP 处理请求。
func (h *ChatCompletionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, apperr.New(apperr.CodeInvalidParam, "only POST is allowed"))
		return
	}
	keyID := apiKeyIDFromContext(r.Context())
	if h.limiter != nil && !h.limiter.Allow(keyID) {
		writeErr(w, apperr.New(apperr.CodeRateLimited, "api key qps exceeded"))
		return
	}
	owner := userFromContext(r.Context())

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "read request body failed", err))
		return
	}
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "invalid json body", err))
		return
	}
	m, err := model.Parse(req.Model)
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(req.Messages) == 0 && strings.TrimSpace(req.Model) == "" {
		writeErr(w, apperr.New(apperr.CodeInvalidParam, "messages is required"))
		return
	}

	mode := task.ParseMode(headerIgnoreCase(r, ModeHeader))
	operation := task.ParseOperation(headerIgnoreCase(r, OperationHeader))
	workDir := headerIgnoreCase(r, WorkDirHeader)
	agentID := headerIgnoreCase(r, AgentHeader)

	// 权限 = min(秘钥上限, 请求头声明)。read 秘钥无法靠请求头提权。
	keyPerm := task.Permission(apiPermissionFromContext(r.Context()))
	perm := keyPerm
	if raw := headerIgnoreCase(r, PermissionHeader); raw != "" {
		reqPerm, ok := task.ParsePermission(raw)
		if !ok {
			writeErr(w, apperr.New(apperr.CodeInvalidParam,
				"invalid permission, allowed values: read | write | all"))
			return
		}
		perm = task.RestrictPermission(keyPerm, reqPerm)
	}

	cmd := gateway.SubmitTaskCommand{
		OwnerID:     owner.ID(),
		APIKeyID:    keyID,
		AgentID:     agentIDOf(agentID),
		Model:       m,
		Messages:    toDomainMessages(req.Messages),
		Operation:   operation,
		WorkDir:     workDir,
		Stream:      req.Stream,
		Mode:        mode,
		Temperature: req.Temperature,
		MaxTokens:   derefInt(req.MaxTokens),
		Permission:  perm,
	}

	result, err := h.submit.Execute(r.Context(), cmd)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer result.Close()

	w.Header().Set(TaskIDHeader, result.TaskID)
	w.Header().Set(ModeHeader, result.Mode.String())

	if result.Stream {
		h.writeStream(w, r, result)
		return
	}
	h.writeBlocking(w, r, result)
}

// writeStream SSE 流式输出：把任务事件实时转成 OpenAI chunk。
func (h *ChatCompletionHandler) writeStream(w http.ResponseWriter, r *http.Request, res *gateway.SubmitTaskResult) {
	sse, err := NewSSEWriter(w)
	if err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInternal, "init sse failed", err))
		return
	}
	created := res.CreatedAt
	idle := h.policy.StreamIdleTimeout
	timer := time.NewTimer(idle)
	defer timer.Stop()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	// sent 累计已下发的增量，用于从完整结果中补齐尾部，避免重复输出。
	var sent strings.Builder

	for {
		select {
		case <-r.Context().Done():
			h.log.Debug("client disconnected", port.F("task_id", res.TaskID))
			return
		case ev, ok := <-res.Events:
			if !ok {
				sse.Done()
				return
			}
			switch ev.Type {
			case port.EventChunk:
				sent.WriteString(ev.Content)
				chunk := openai.StreamChunk{
					ID:      res.TaskID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   res.Model.String(),
					Choices: []openai.StreamChoice{{
						Index: 0,
						Delta: openai.Delta{Role: openai.RoleAssistant, Content: ev.Content},
					}},
				}
				if err := sse.WriteChunk(chunk); err != nil {
					return
				}
			case port.EventDone:
				reason := "stop"
				// Done 事件携带完整结果，仅补齐尚未下发的尾部增量。
				if delta := strings.TrimPrefix(ev.Content, sent.String()); delta != "" {
					_ = sse.WriteChunk(openai.StreamChunk{
						ID:      res.TaskID,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   res.Model.String(),
						Choices: []openai.StreamChoice{{
							Index: 0,
							Delta: openai.Delta{Role: openai.RoleAssistant, Content: delta},
						}},
					})
				}
				chunk := openai.StreamChunk{
					ID:      res.TaskID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   res.Model.String(),
					Choices: []openai.StreamChoice{{Index: 0, Delta: openai.Delta{}, FinishReason: &reason}},
				}
				_ = sse.WriteChunk(chunk)
				sse.Done()
				return
			case port.EventError, port.EventRejected:
				_ = sse.WriteEvent("error", mustJSON(openai.ErrorResponse{
					Error: openai.ErrorDetail{
						Message: ev.Message,
						Type:    ev.Code,
						Code:    ev.Code,
					},
				}))
				sse.Done()
				return
			}
			if !resetTimer(timer, idle) {
				return
			}
		case <-timer.C:
			h.log.Warn("stream idle timeout", port.F("task_id", res.TaskID))
			_ = sse.WriteEvent("error", mustJSON(openai.ErrorResponse{
				Error: openai.ErrorDetail{Message: "stream idle timeout", Type: "timeout", Code: "timeout"},
			}))
			sse.Done()
			return
		case <-keepAlive.C:
			// 注释行保活，避免反向代理掐断空闲 SSE。
			_ = sse.WriteComment("keep-alive")
		}
	}
}

// writeBlocking 非流式：等待任务终止事件后一次性返回。
func (h *ChatCompletionHandler) writeBlocking(w http.ResponseWriter, r *http.Request, res *gateway.SubmitTaskResult) {
	timeout := h.policy.RequestTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var sb strings.Builder
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-res.Events:
			if !ok {
				writeErr(w, apperr.New(apperr.CodeInternal, "task event stream closed unexpectedly"))
				return
			}
			switch ev.Type {
			case port.EventChunk:
				sb.WriteString(ev.Content)
			case port.EventDone:
				content := ev.Content
				if content == "" {
					content = sb.String()
				}
				writeJSON(w, http.StatusOK, openai.ChatCompletionResponse{
					ID:      res.TaskID,
					Object:  "chat.completion",
					Created: res.CreatedAt,
					Model:   res.Model.String(),
					Choices: []openai.Choice{{
						Index:        0,
						Message:      openai.Message{Role: openai.RoleAssistant, Content: content},
						FinishReason: "stop",
					}},
				})
				return
			case port.EventError, port.EventRejected:
				writeErr(w, apperr.New(apperr.Code(ev.Code), ev.Message))
				return
			}
			if !resetTimer(timer, timeout) {
				return
			}
		case <-timer.C:
			writeErr(w, apperr.New(apperr.CodeTimeout, "task did not finish within "+timeout.String()))
			return
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) bool {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	if d <= 0 {
		return true
	}
	t.Reset(d)
	return true
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":{"message":"internal error"}}`)
	}
	return raw
}

func toDomainMessages(msgs []openai.Message) []task.Message {
	out := make([]task.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, task.Message{Role: string(m.Role), Content: m.Content})
	}
	return out
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func headerIgnoreCase(r *http.Request, name string) string {
	if v := r.Header.Get(name); v != "" {
		return v
	}
	lower := strings.ToLower(name)
	for k, vals := range r.Header {
		if strings.ToLower(k) == lower && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}
