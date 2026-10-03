package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/ratelimit"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// Server 网关 HTTP 服务（基于 net/http，底层由 Go runtime 的 epoll/kqueue IO 多路复用驱动）。
type Server struct {
	cfg           *config.GatewayConfig
	httpServer    *http.Server
	registry      *gateway.AgentRegistry
	hub           *ws.Hub
	keyLimiter    *ratelimit.KeyedLimiter
	globalLimiter *ratelimit.GlobalLimiter
	queueRepo     QueueStats
	log           port.Logger
	// serveCtx 长连接与后台任务的根上下文，随服务关闭取消。
	serveCtx    context.Context
	serveCancel context.CancelFunc
}

// QueueStats 队列统计能力（运维接口用）。
type QueueStats interface {
	Snapshot() map[string]int
}

// Deps 构造网关服务所需的依赖。
type Deps struct {
	Config    *config.GatewayConfig
	Registry  *gateway.AgentRegistry
	Submit    *gateway.SubmitTaskUseCase
	Pull      *gateway.PullTasksUseCase
	Ack       *gateway.AckTaskUseCase
	Health    *gateway.ReportHealthUseCase
	Hub       *ws.Hub
	QueueRepo QueueStats
	Logger    port.Logger
	Policy    gateway.TaskPolicy
	// TaskRepo 任务仓储（控制台任务列表）。
	TaskRepo task.TaskRepository
	// Chat 网页对话用例。
	Chat *gateway.ChatUseCase
	// Bots 机器人管理用例。
	Bots *gateway.BotAdminUseCase
	// BotInbound 机器人入站消息用例。
	BotInbound *gateway.BotInboundUseCase
	// BotRepo 机器人仓储（回调定位配置）。
	BotRepo bot.BotRepository
}

// NewServer 构造网关服务并装配路由。
func NewServer(d Deps) *Server {
	cfg := d.Config
	keyLimiter := ratelimit.NewKeyedLimiter(cfg.RateLimit.QPS, cfg.RateLimit.Burst, 10000)
	globalLimiter := ratelimit.NewGlobalLimiter(cfg.RateLimit.MaxInflight)

	chat := NewChatCompletionHandler(d.Submit, keyLimiter, d.Logger, d.Policy)
	serveCtx, serveCancel := context.WithCancel(context.Background())
	agentHandlers := NewAgentHandlers(d.Pull, d.Ack, d.Health, d.Registry, d.Hub, d.Logger).
		WithBaseContext(serveCtx)

	s := &Server{
		cfg:           cfg,
		registry:      d.Registry,
		hub:           d.Hub,
		keyLimiter:    keyLimiter,
		globalLimiter: globalLimiter,
		queueRepo:     d.QueueRepo,
		log:           d.Logger.With(port.F("cmp", "http_server")),
		serveCtx:      serveCtx,
		serveCancel:   serveCancel,
	}

	apiKeyAuth := NewAPIKeyAuthenticator(cfg.Security.APIKeys)
	agentAuth := NewAgentTokenAuthenticator(func(ctx context.Context, token string) (*agent.Agent, error) {
		return d.Registry.Authenticate(ctx, token)
	})
	adminAuth := NewAdminAuthenticator(adminTokenOf(cfg))

	mux := http.NewServeMux()
	// 对外业务接口（OpenAI 兼容）
	mux.HandleFunc("POST /v1/chat/completions", s.withCommon(WithAPIKeyAuth(apiKeyAuth, chat.ServeHTTP)))
	// Agent 内部接口
	mux.HandleFunc("GET /agent/pull", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Pull)))
	mux.HandleFunc("POST /agent/ack", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Ack)))
	mux.HandleFunc("POST /agent/health", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Health)))
	mux.HandleFunc("GET /agent/ws", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.WebSocket)))
	// 运维接口
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /admin/agents", s.withCommon(WithAPIKeyAuth(apiKeyAuth, s.listAgents)))
	mux.HandleFunc("GET /admin/queues", s.withCommon(WithAPIKeyAuth(apiKeyAuth, s.listQueues)))

	// --- 网页控制台：管理 API ---
	if d.Bots != nil {
		bots := NewBotHandlers(d.Bots, cfg.Server.PublicAddr, d.Logger)
		mux.HandleFunc("GET /api/bots", s.withCommon(WithAdminAuth(adminAuth, bots.List)))
		mux.HandleFunc("POST /api/bots", s.withCommon(WithAdminAuth(adminAuth, bots.Create)))
		mux.HandleFunc("GET /api/bots/{id}", s.withCommon(WithAdminAuth(adminAuth, bots.Get)))
		mux.HandleFunc("PUT /api/bots/{id}", s.withCommon(WithAdminAuth(adminAuth, bots.Update)))
		mux.HandleFunc("DELETE /api/bots/{id}", s.withCommon(WithAdminAuth(adminAuth, bots.Delete)))
		mux.HandleFunc("POST /api/bots/{id}/toggle", s.withCommon(WithAdminAuth(adminAuth, bots.Toggle)))
	}
	if d.Chat != nil {
		chatWeb := NewChatHandler(d.Chat, d.Policy, d.Logger)
		mux.HandleFunc("POST /api/chat", s.withCommon(WithAdminAuth(adminAuth, chatWeb.ServeHTTP)))
	}
	if d.Registry != nil {
		console := NewConsoleHandlers(d.Registry, d.TaskRepo, d.QueueRepo, d.BotRepo, d.Hub, d.Logger)
		mux.HandleFunc("GET /api/overview", s.withCommon(WithAdminAuth(adminAuth, console.Overview)))
		mux.HandleFunc("GET /api/agents", s.withCommon(WithAdminAuth(adminAuth, console.Agents)))
		mux.HandleFunc("GET /api/tasks", s.withCommon(WithAdminAuth(adminAuth, console.Tasks)))
		mux.HandleFunc("GET /api/models", s.withCommon(WithAdminAuth(adminAuth, console.Models)))
	}
	mux.HandleFunc("POST /api/auth/login", s.withCommon(func(w http.ResponseWriter, r *http.Request) {
		if err := adminAuth.Authenticate(r); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "auth_required": adminAuth.Enabled()})
	}))

	// --- IM 机器人回调（由平台调用，不走网关自身鉴权） ---
	if d.BotRepo != nil && d.BotInbound != nil {
		hooks := NewWebhookHandlers(d.BotRepo, d.BotInbound, d.Logger)
		mux.HandleFunc("POST /webhook/feishu/{id}", s.withCommon(hooks.Feishu))
		mux.HandleFunc("GET /webhook/wecom/{id}", s.withCommon(hooks.Wecom))
		mux.HandleFunc("POST /webhook/wecom/{id}", s.withCommon(hooks.Wecom))
	}

	// --- 前端静态资源（Vue 构建产物） ---
	if spa := NewSPAHandler(cfg.Web.StaticDir); spa != nil && cfg.Web.Enabled {
		mux.Handle("/", spa)
	} else {
		mux.HandleFunc("GET /", s.indexPlaceholder)
	}

	s.httpServer = &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.Background() },
	}
	return s
}

// withCommon 通用中间件：panic 恢复 + 全局在途限流 + 访问日志。
func (s *Server) withCommon(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered", port.F("panic", rec), port.F("path", r.URL.Path))
				writeErr(w, apperr.New(apperr.CodeInternal, "internal server error"))
			}
			s.log.Debug("request done",
				port.F("method", r.Method),
				port.F("path", r.URL.Path),
				port.F("cost_ms", time.Since(start).Milliseconds()))
		}()

		if s.globalLimiter != nil {
			if err := s.globalLimiter.Acquire(r.Context()); err != nil {
				writeErr(w, apperr.New(apperr.CodeRateLimited, "gateway is busy, too many inflight requests"))
				return
			}
			defer s.globalLimiter.Release()
		}
		next(w, r)
	}
}

// Run 启动服务并阻塞，直到 ctx 取消或发生致命错误。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("gateway listening", port.F("addr", s.cfg.Server.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.Shutdown()
	}
}

// Shutdown 优雅退出。
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer cancel()
	defer s.serveCancel()
	s.log.Info("gateway shutting down")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"service":  "codeporter-gateway",
		"ws_conns": connCount(s.hub),
	})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	list, err := s.registry.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		out = append(out, map[string]any{
			"id":             string(a.ID()),
			"name":           a.Name(),
			"status":         a.Status().String(),
			"last_heartbeat": a.LastHeartbeatAt().Unix(),
			"mcps":           a.Health().MCPs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *Server) listQueues(w http.ResponseWriter, _ *http.Request) {
	snapshot := map[string]int{}
	if s.queueRepo != nil {
		snapshot = s.queueRepo.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{"queues": snapshot})
}

func connCount(h *ws.Hub) int {
	if h == nil {
		return 0
	}
	return h.ConnCount()
}

// adminTokenOf 管理端令牌：未显式配置时回落到第一个 API Key。
func adminTokenOf(cfg *config.GatewayConfig) string {
	if t := strings.TrimSpace(cfg.Security.AdminToken); t != "" {
		return t
	}
	if len(cfg.Security.APIKeys) > 0 {
		return cfg.Security.APIKeys[0]
	}
	return ""
}

// indexPlaceholder 未托管前端时的根路径提示。
func (s *Server) indexPlaceholder(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "codeporter-gateway",
		"docs":    "/healthz",
		"hint":    "web console is not bundled; build frontend and set web.static_dir",
	})
}
