package http

import (
	"net/http"
	"strconv"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
)

// ConsoleHandlers 网页控制台的数据接口：概览、Agent、任务、模型。
type ConsoleHandlers struct {
	registry  *gateway.AgentRegistry
	taskRepo  task.TaskRepository
	queueRepo QueueStats
	botRepo   bot.BotRepository
	hub       *ws.Hub
	log       port.Logger
}

// NewConsoleHandlers 构造处理器。
func NewConsoleHandlers(
	registry *gateway.AgentRegistry,
	taskRepo task.TaskRepository,
	queueRepo QueueStats,
	botRepo bot.BotRepository,
	hub *ws.Hub,
	log port.Logger,
) *ConsoleHandlers {
	return &ConsoleHandlers{
		registry:  registry,
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		botRepo:   botRepo,
		hub:       hub,
		log:       log.With(port.F("h", "console")),
	}
}

// Overview 首页概览数据。
func (h *ConsoleHandlers) Overview(w http.ResponseWriter, r *http.Request) {
	agents, err := h.registry.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	online := 0
	for _, a := range agents {
		if a.IsAvailable() {
			online++
		}
	}
	queues := map[string]int{}
	if h.queueRepo != nil {
		queues = h.queueRepo.Snapshot()
	}
	stats := h.taskStats(r)

	bots := 0
	enabledBots := 0
	if h.botRepo != nil {
		list, err := h.botRepo.FindAll(r.Context())
		if err == nil {
			bots = len(list)
			for _, b := range list {
				if b.Enabled() {
					enabledBots++
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agents":        len(agents),
		"agents_online": online,
		"ws_conns":      connCount(h.hub),
		"queues":        queues,
		"tasks":         stats,
		"bots":          bots,
		"bots_enabled":  enabledBots,
		"models":        h.modelViews(agents),
	})
}

// Agents Agent 列表。
func (h *ConsoleHandlers) Agents(w http.ResponseWriter, r *http.Request) {
	list, err := h.registry.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		queued := 0
		if h.queueRepo != nil {
			queued = h.queueRepo.Snapshot()[string(a.ID())]
		}
		out = append(out, map[string]any{
			"id":              string(a.ID()),
			"name":            a.Name(),
			"status":          a.Status().String(),
			"last_heartbeat":  a.LastHeartbeatAt().Unix(),
			"queued":          queued,
			"max_concurrency": a.MaxConcurrency(),
			"health":          a.Health(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// Tasks 任务列表（默认返回最近 50 条）。
func (h *ConsoleHandlers) Tasks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	agentID := r.URL.Query().Get("agent_id")

	var list []*task.Task
	var err error
	if agentID != "" {
		list, err = h.taskRepo.FindByAgent(r.Context(), agent.ID(agentID))
	} else {
		list, err = h.taskRepo.FindByStatus(r.Context(), task.AllStatuses()...)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	// 按更新时间倒序取最近 N 条。
	sortTasksByUpdatedDesc(list)
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, map[string]any{
			"id":         string(t.ID()),
			"agent_id":   string(t.AgentID()),
			"model":      t.Model().String(),
			"mode":       t.Mode().String(),
			"status":     t.Status().String(),
			"attempts":   t.Attempts(),
			"created_at": t.CreatedAt().Unix(),
			"updated_at": t.UpdatedAt().Unix(),
			"chunks":     t.Seq(),
			"error":      t.ErrorMessage(),
			"source":     t.APIKeyID(),
			"prompt":     preview(t.Request().FlattenPrompt(), 120),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out, "total": len(out)})
}

// Models 可用模型及其在各 Agent 上的健康状态。
func (h *ConsoleHandlers) Models(w http.ResponseWriter, r *http.Request) {
	agents, err := h.registry.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": h.modelViews(agents)})
}

// modelViews 汇总模型可用情况。
func (h *ConsoleHandlers) modelViews(agents []*agent.Agent) []map[string]any {
	out := make([]map[string]any, 0, len(model.All()))
	for _, m := range model.All() {
		available := false
		details := make([]map[string]any, 0, len(agents))
		for _, a := range agents {
			for _, mcp := range a.Health().MCPs {
				if mcp.Model != m {
					continue
				}
				if mcp.Available {
					available = true
				}
				details = append(details, map[string]any{
					"agent":     string(a.ID()),
					"available": mcp.Available,
					"detail":    mcp.Detail,
				})
			}
		}
		out = append(out, map[string]any{
			"model":     m.String(),
			"available": available,
			"agents":    details,
		})
	}
	return out
}

// taskStats 统计各状态任务数。
func (h *ConsoleHandlers) taskStats(r *http.Request) map[string]int {
	stats := map[string]int{}
	if h.taskRepo == nil {
		return stats
	}
	for _, st := range task.AllStatuses() {
		list, err := h.taskRepo.FindByStatus(r.Context(), st)
		if err != nil {
			continue
		}
		stats[st.String()] = len(list)
	}
	return stats
}

// preview 截取预览文本。
func preview(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// sortTasksByUpdatedDesc 按更新时间倒序排序。
func sortTasksByUpdatedDesc(list []*task.Task) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].UpdatedAt().After(list[j-1].UpdatedAt()); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
