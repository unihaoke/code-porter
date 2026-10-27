package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// CallbackStatus 回调体中的任务状态。
type CallbackStatus string

const (
	// CallbackSuccess 任务成功。
	CallbackSuccess CallbackStatus = "success"
	// CallbackFailed 任务失败（含失败/超时/死信）。
	CallbackFailed CallbackStatus = "failed"
)

// CallbackPayload 回调请求体。
type CallbackPayload struct {
	TaskID   string         `json:"task_id"`
	JobID    string         `json:"job_id"`
	TenantID string         `json:"tenant_id"`
	Status   CallbackStatus `json:"status"`
	Content  string         `json:"content"`
	Error    string         `json:"error"`
}

// statusFromTask 把领域终态映射为回调状态：成功 → success，其余终态 → failed。
func statusFromTask(s task.Status) CallbackStatus {
	if s == task.StatusSuccess {
		return CallbackSuccess
	}
	return CallbackFailed
}

// CallbackDispatcher 封装任务终态回调逻辑：向调用方 callback_url 发送 POST。
//
// 回调在独立 goroutine 中异步执行，不阻塞调用方（ACK / 生命周期守护）流程。
// 失败只记日志不重试——调用方有超时兜底。
type CallbackDispatcher struct {
	client  port.HTTPClient
	timeout time.Duration
	log     port.Logger
}

// NewCallbackDispatcher 构造回调分发器。
//
// client 为 nil 时使用 http.DefaultClient；timeout <= 0 时默认 10s。
func NewCallbackDispatcher(client port.HTTPClient, timeout time.Duration, log port.Logger) *CallbackDispatcher {
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &CallbackDispatcher{
		client:  client,
		timeout: timeout,
		log:     log.With(port.F("cmp", "callback_dispatcher")),
	}
}

// Dispatch 异步触发任务终态回调。callback_url 为空时直接跳过。
//
// 该方法立即返回，回调在后台 goroutine 中执行；失败仅记日志。
func (d *CallbackDispatcher) Dispatch(t *task.Task) {
	if d == nil || t == nil {
		return
	}
	url := t.CallbackURL()
	if url == "" {
		return
	}

	payload := CallbackPayload{
		TaskID:   string(t.ID()),
		JobID:    t.JobID(),
		TenantID: t.TenantID(),
		Status:   statusFromTask(t.Status()),
		Content:  t.Result(),
		Error:    t.ErrorMessage(),
	}

	go d.post(url, t.CallbackToken(), payload)
}

func (d *CallbackDispatcher) post(url, token string, payload CallbackPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		d.log.Error("marshal callback payload failed",
			port.F("task_id", payload.TaskID), port.F("err", err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		d.log.Error("build callback request failed",
			port.F("task_id", payload.TaskID), port.F("url", url), port.F("err", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.log.Warn("callback request failed",
			port.F("task_id", payload.TaskID),
			port.F("url", url),
			port.F("status", payload.Status),
			port.F("err", err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// 读取响应体仅为日志/排错，丢弃；非 2xx 记警告，不重试。
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)); err != nil {
		d.log.Debug("read callback response failed",
			port.F("task_id", payload.TaskID), port.F("err", err.Error()))
	}
	if resp.StatusCode >= 300 {
		d.log.Warn("callback returned non-2xx",
			port.F("task_id", payload.TaskID),
			port.F("url", url),
			port.F("status", payload.Status),
			port.F("http_status", resp.StatusCode))
		return
	}
	d.log.Info("callback delivered",
		port.F("task_id", payload.TaskID),
		port.F("url", url),
		port.F("status", payload.Status),
		port.F("http_status", resp.StatusCode))
}
