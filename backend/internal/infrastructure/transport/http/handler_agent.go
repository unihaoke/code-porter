package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// AgentHandlers Agent 内部接口集合（Pull / ACK / 健康上报 / WebSocket 直连）。
type AgentHandlers struct {
	pull     *gateway.PullTasksUseCase
	ack      *gateway.AckTaskUseCase
	health   *gateway.ReportHealthUseCase
	registry *gateway.AgentRegistry
	hub      *ws.Hub
	log      port.Logger
	// baseCtx 长连接使用的根上下文。
	//
	// 注意：WebSocket 升级后 HTTP handler 立即返回，r.Context() 会被取消，
	// 因此必须用一个与请求无关、随服务关闭才取消的上下文来驱动连接读循环。
	baseCtx context.Context
}

// WithBaseContext 设置长连接根上下文，返回自身便于链式装配。
func (h *AgentHandlers) WithBaseContext(ctx context.Context) *AgentHandlers {
	h.baseCtx = ctx
	return h
}

// NewAgentHandlers 构造 Agent 接口处理器。
func NewAgentHandlers(
	pull *gateway.PullTasksUseCase,
	ack *gateway.AckTaskUseCase,
	health *gateway.ReportHealthUseCase,
	registry *gateway.AgentRegistry,
	hub *ws.Hub,
	log port.Logger,
) *AgentHandlers {
	return &AgentHandlers{
		pull: pull, ack: ack, health: health, registry: registry, hub: hub,
		log: log.With(port.F("h", "agent_api")),
	}
}

// Pull 处理 GET /agent/pull?agentId=xxx&maxBatch=n。
func (h *AgentHandlers) Pull(w http.ResponseWriter, r *http.Request) {
	ag := agentFrom(r.Context())
	if ag == nil {
		writeErr(w, apperr.New(apperr.CodeUnauthorized, "agent not authenticated"))
		return
	}
	agentID := r.URL.Query().Get("agentId")
	if agentID == "" {
		agentID = string(ag.ID())
	}
	if agentID != string(ag.ID()) {
		writeErr(w, apperr.New(apperr.CodeForbidden, "agentId does not match token"))
		return
	}
	maxBatch, _ := strconv.Atoi(r.URL.Query().Get("maxBatch"))
	if maxBatch <= 0 {
		maxBatch = gateway.DefaultPullBatch
	}

	res, err := h.pull.Execute(r.Context(), gateway.PullTasksQuery{
		AgentID:  ag.ID(),
		MaxBatch: maxBatch,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if res.Tasks == nil {
		res.Tasks = []port.TaskDispatch{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":     res.Tasks,
		"queue_len": res.QueueLen,
		"timestamp": time.Now().Unix(),
	})
}

// Ack 处理 POST /agent/ack。
func (h *AgentHandlers) Ack(w http.ResponseWriter, r *http.Request) {
	ag := agentFrom(r.Context())
	if ag == nil {
		writeErr(w, apperr.New(apperr.CodeUnauthorized, "agent not authenticated"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "read body failed", err))
		return
	}
	var req port.AckRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "invalid json body", err))
		return
	}
	if req.TaskID == "" {
		writeErr(w, apperr.New(apperr.CodeInvalidParam, "taskId is required"))
		return
	}

	res, err := h.ack.Execute(r.Context(), gateway.AckCommand{
		TaskID:    req.TaskID,
		AgentID:   ag.ID(),
		LockToken: req.LockToken,
		Status:    req.Status,
		Chunks:    req.Chunks,
		Error:     req.Error,
		Result:    req.Result,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"status":      res.Status.String(),
		"retry":       res.Retry,
		"dead_letter": res.DeadLetter,
		"chunks":      res.AcceptedChunks,
	})
}

// Health 处理 POST /agent/health。
func (h *AgentHandlers) Health(w http.ResponseWriter, r *http.Request) {
	ag := agentFrom(r.Context())
	if ag == nil {
		writeErr(w, apperr.New(apperr.CodeUnauthorized, "agent not authenticated"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "read body failed", err))
		return
	}
	var req struct {
		AgentID string       `json:"agent_id"`
		Health  agent.Health `json:"health"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, apperr.Wrap(apperr.CodeInvalidParam, "invalid json body", err))
		return
	}
	if err := h.health.Execute(r.Context(), gateway.ReportHealthCommand{
		AgentID: ag.ID(),
		Health:  req.Health,
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// WebSocket 处理 GET /agent/ws?agentId=xxx：升级为直连长连接。
func (h *AgentHandlers) WebSocket(w http.ResponseWriter, r *http.Request) {
	ag := agentFrom(r.Context())
	if ag == nil {
		writeErr(w, apperr.New(apperr.CodeUnauthorized, "agent not authenticated"))
		return
	}
	agentID := r.URL.Query().Get("agentId")
	if agentID == "" {
		agentID = string(ag.ID())
	}
	if agentID != string(ag.ID()) {
		writeErr(w, apperr.New(apperr.CodeForbidden, "agentId does not match token"))
		return
	}
	if h.hub == nil {
		writeErr(w, apperr.New(apperr.CodeUnavailable, "direct mode is disabled"))
		return
	}
	connCtx := h.baseCtx
	if connCtx == nil {
		connCtx = r.Context()
	}
	if err := h.hub.Upgrade(connCtx, w, r, agentID); err != nil {
		h.log.Error("websocket upgrade failed", port.F("err", err.Error()))
		return
	}
	if err := h.registry.MarkOnline(r.Context(), ag.ID()); err != nil {
		h.log.Warn("mark agent online failed", port.F("err", err.Error()))
	}
}

// agentIDOf 把字符串转换为 Agent ID。
func agentIDOf(s string) agent.ID { return agent.ID(strings.TrimSpace(s)) }
