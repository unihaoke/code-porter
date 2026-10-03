package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/codeporter/code-porter/pkg/openai"
)

// SSEWriter SSE（Server-Sent Events）写出器。
//
// 用于把本地 AI 的流式输出实时透传给外部调用方（PRD：适配 Cursor 等各类 Agent 客户端）。
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter 构造写出器并设置响应头；不支持 Flush 时返回错误。
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported by this connection")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // 禁用 Nginx 缓冲
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &SSEWriter{w: w, flusher: flusher}, nil
}

// WriteEvent 写一条自定义事件。
func (s *SSEWriter) WriteEvent(event string, data []byte) error {
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// WriteChunk 写一段 OpenAI 格式的流式增量。
func (s *SSEWriter) WriteChunk(chunk openai.StreamChunk) error {
	raw, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	return s.WriteEvent("", raw)
}

// WriteComment 写注释行，用于保活（防止中间代理因空闲断开连接）。
func (s *SSEWriter) WriteComment(comment string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", comment); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// Done 写流结束标记。
func (s *SSEWriter) Done() {
	_, _ = fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flusher.Flush()
}
