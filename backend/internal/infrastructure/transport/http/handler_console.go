package http

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// ConsoleHandlers 网页控制台的数据接口：概览、Agent、任务、模型。
//
// 普通用户只能看到自己名下的数据；admin 看到全局并带 owner 字段。
type ConsoleHandlers struct {
	registry  *gateway.AgentRegistry
	taskRepo  task.TaskRepository
	queueRepo QueueStats
	botRepo   bot.BotRepository
	users     user.Repository
	hub       connCounter
	log       port.Logger
}

// NewConsoleHandlers 构造处理器。
func NewConsoleHandlers(registry *gateway.AgentRegistry, taskRepo task.TaskRepository,
	queueRepo QueueStats, botRepo bot.BotRepository, users user.Repository, hub connCounter, log port.Logger) *ConsoleHandlers {
	return &ConsoleHandlers{
		registry:  registry,
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		botRepo:   botRepo,
		users:     users,
		hub:       hub,
		log:       log.With(port.F("h", "console")),
	}
}

// scope 本次请求的数据可见范围。
type scope struct {
	actor   *user.User
	isAdmin bool
}

func scopeFrom(r *http.Request) scope {
	u := userFromContext(r.Context())
	return scope{actor: u, isAdmin: u != nil && u.IsAdmin()}
}

// visibleAgents 按租户返回可见实例（admin 全部）。
func (h *ConsoleHandlers) visibleAgents(r *http.Request, sc scope) ([]*agent.Agent, error) {
	if sc.isAdmin {
		return h.registry.List(r.Context())
	}
	return h.registry.ListForOwner(r.Context(), sc.actor.ID())
}

// visibleBots 按租户返回可见机器人。
func (h *ConsoleHandlers) visibleBots(r *http.Request, sc scope) ([]*bot.Bot, error) {
	if sc.isAdmin {
		return h.botRepo.FindAll(r.Context())
	}
	return h.botRepo.FindByOwner(r.Context(), sc.actor.ID())
}

// usernameMap 批量构造 userID → username（admin 视图用）。
func (h *ConsoleHandlers) usernameMap(r *http.Request, agents []*agent.Agent) map[string]string {
	out := make(map[string]string, len(agents))
	for _, a := range agents {
		out[string(a.OwnerID())] = string(a.OwnerID())
	}
	for id := range out {
		if u, err := h.users.FindByID(r.Context(), user.ID(id)); err == nil {
			out[id] = u.Username()
		}
	}
	return out
}

// Overview 首页概览数据。
func (h *ConsoleHandlers) Overview(w http.ResponseWriter, r *http.Request) {
	sc := scopeFrom(r)
	agents, err := h.visibleAgents(r, sc)
	if err != nil {
		writeErr(w, err)
		return
	}
	statuses := task.AllStatuses()
	var tasks []*task.Task
	if sc.isAdmin {
		tasks, err = h.taskRepo.FindByStatus(r.Context(), statuses...)
	} else {
		tasks, err = h.taskRepo.FindByOwner(r.Context(), sc.actor.ID(), statuses...)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	stats := h.taskStats(tasks, agents)
	botsCount := 0
	if h.botRepo != nil {
		if bots, bErr := h.visibleBots(r, sc); bErr == nil {
			botsCount = len(bots)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":        len(agents),
		"agents_online": stats["agents_online"],
		"tasks":         stats,
		"queues":        h.queueSnapshot(agents),
		"ws_conns":      connCount(h.hub),
		"bots":          botsCount,
		"models":        h.modelViews(agents),
	})
}

// Agents Agent 列表。
func (h *ConsoleHandlers) Agents(w http.ResponseWriter, r *http.Request) {
	sc := scopeFrom(r)
	list, err := h.visibleAgents(r, sc)
	if err != nil {
		writeErr(w, err)
		return
	}
	names := h.usernameMap(r, list)
	snap := h.queueSnapshot(list)
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		row := map[string]any{
			"id":              string(a.ID()),
			"name":            a.Name(),
			"status":          a.Status().String(),
			"last_heartbeat":  a.LastHeartbeatAt().Unix(),
			"queued":          snap[string(a.ID())],
			"max_concurrency": a.MaxConcurrency(),
		}
		if sc.isAdmin {
			row["owner_id"] = string(a.OwnerID())
			row["owner"] = names[string(a.OwnerID())]
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// Tasks 任务列表（默认返回最近 50 条）。
func (h *ConsoleHandlers) Tasks(w http.ResponseWriter, r *http.Request) {
	sc := scopeFrom(r)
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	agentID := agent.ID(r.URL.Query().Get("agent_id"))

	var list []*task.Task
	var err error
	switch {
	case agentID != "":
		// 指定实例时同样执行租户校验：非属主得到空列表。
		owned, aErr := h.visibleAgents(r, sc)
		if aErr != nil {
			writeErr(w, aErr)
			return
		}
		if !containsAgent(owned, agentID) {
			list = nil
		} else {
			list, err = h.taskRepo.FindByAgent(r.Context(), agentID)
		}
	case sc.isAdmin:
		list, err = h.taskRepo.FindByStatus(r.Context(), task.AllStatuses()...)
	default:
		list, err = h.taskRepo.FindByOwner(r.Context(), sc.actor.ID(), task.AllStatuses()...)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	sortTasksByUpdatedDesc(list)
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		row := map[string]any{
			"id":         string(t.ID()),
			"agent_id":   string(t.AgentID()),
			"model":      t.Model().String(),
			"status":     t.Status().String(),
			"attempts":   t.Attempts(),
			"created_at": t.CreatedAt().Unix(),
			"updated_at": t.UpdatedAt().Unix(),
			"prompt":     preview(t.Request().FlattenPrompt(), 120),
			"error":      t.ErrorMessage(),
			"source":     t.APIKeyID(),
		}
		if sc.isAdmin {
			row["owner_id"] = string(t.OwnerID())
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out, "total": len(out)})
}

// Models 可用模型及其在各可见实例上的健康状态。
func (h *ConsoleHandlers) Models(w http.ResponseWriter, r *http.Request) {
	sc := scopeFrom(r)
	agents, err := h.visibleAgents(r, sc)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": h.modelViews(agents)})
}

func (h *ConsoleHandlers) modelViews(agents []*agent.Agent) []map[string]any {
	out := make([]map[string]any, 0)
	for _, m := range model.All() {
		details := make([]map[string]any, 0)
		available := false
		for _, a := range agents {
			for _, mcp := range a.Health().MCPs {
				if mcp.Model != m {
					continue
				}
				if mcp.Available {
					available = true
				}
				details = append(details, map[string]any{
					"agent":      string(a.ID()),
					"agent_name": a.Name(),
					"available":  mcp.Available,
					"detail":     mcp.Detail,
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

// queueSnapshot 只统计可见实例的队列长度。
func (h *ConsoleHandlers) queueSnapshot(agents []*agent.Agent) map[string]int {
	out := map[string]int{}
	if h.queueRepo == nil {
		return out
	}
	snap := h.queueRepo.Snapshot()
	for _, a := range agents {
		out[string(a.ID())] = snap[string(a.ID())]
	}
	return out
}

// taskStats 在给定任务集合内统计各状态任务数与在线实例数。
func (h *ConsoleHandlers) taskStats(list []*task.Task, agents []*agent.Agent) map[string]int {
	stats := map[string]int{}
	for _, st := range task.AllStatuses() {
		stats[st.String()] = 0
	}
	for _, t := range list {
		stats[t.Status().String()]++
	}
	online := 0
	for _, a := range agents {
		if a.IsAvailable() {
			online++
		}
	}
	stats["agents_online"] = online
	return stats
}

func containsAgent(list []*agent.Agent, id agent.ID) bool {
	for _, a := range list {
		if a.ID() == id {
			return true
		}
	}
	return false
}

// sortTasksByUpdatedDesc 按更新时间倒序。
func sortTasksByUpdatedDesc(list []*task.Task) {
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt().After(list[j].UpdatedAt()) })
}

// preview 截取提示词预览。
func preview(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// connCount 安全读取 WebSocket 连接数。
func connCount(hub connCounter) int {
	if hub == nil {
		return 0
	}
	return hub.ConnCount()
}
