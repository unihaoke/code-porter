package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
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
	keyLimiter    *ratelimit.KeyedLimiter
	globalLimiter *ratelimit.GlobalLimiter
	queueRepo     QueueStats
	hub           *ws.Hub
	log           port.Logger
	serveCtx      context.Context
	serveCancel   context.CancelFunc
}

// Deps 构造网关服务所需的依赖。
type Deps struct {
	Config     *config.GatewayConfig
	Auth       *authsvc.Service
	Registry   *gateway.AgentRegistry
	Submit     *gateway.SubmitTaskUseCase
	Chat       *gateway.ChatUseCase
	Pull       *gateway.PullTasksUseCase
	Ack        *gateway.AckTaskUseCase
	Health     *gateway.ReportHealthUseCase
	Hub        *ws.Hub
	QueueRepo  QueueStats
	TaskRepo   task.TaskRepository
	Users      user.Repository
	Bots       *gateway.BotAdminUseCase
	BotRepo    bot.BotRepository
	BotInbound *gateway.BotInboundUseCase
	Logger     port.Logger
	Policy     gateway.TaskPolicy
}

// NewServer 构造网关服务并装配路由。
func NewServer(d Deps) *Server {
	cfg := d.Config
	keyLimiter := ratelimit.NewKeyedLimiter(cfg.RateLimit.QPS, cfg.RateLimit.Burst, 10000)
	globalLimiter := ratelimit.NewGlobalLimiter(cfg.RateLimit.MaxInflight)

	serveCtx, serveCancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:           cfg,
		keyLimiter:    keyLimiter,
		globalLimiter: globalLimiter,
		queueRepo:     d.QueueRepo,
		hub:           d.Hub,
		registry:      d.Registry,
		log:           d.Logger.With(port.F("cmp", "http_server")),
		serveCtx:      serveCtx,
		serveCancel:   serveCancel,
	}

	// 鉴权器。
	sessionAuth := NewSessionAuthenticator(d.Auth)
	apiAuth := NewAPIKeyAuth(d.Auth, apikey.ScopeAPI)
	agentAuth := NewAgentAuthenticator(d.Auth, d.Registry)

	chat := NewChatHandler(d.Chat, d.Policy, d.Logger)
	chatCompletion := NewChatCompletionHandler(d.Submit, keyLimiter, d.Logger, d.Policy)
	agentHandlers := NewAgentHandlers(d.Pull, d.Ack, d.Health, d.Registry, d.Hub, d.Logger)
	console := NewConsoleHandlers(d.Registry, d.TaskRepo, d.QueueRepo, d.BotRepo, d.Users, d.Hub, d.Logger)
	authH := NewAuthHandlers(d.Auth, nil, d.Logger)
	usersH := NewUsersHandlers(d.Auth, d.Logger)
	keysH := NewKeysHandlers(d.Auth, d.Logger)

	mux := http.NewServeMux()

	// 对外业务接口（OpenAI 兼容，api scope 秘钥鉴权）。
	mux.HandleFunc("POST /v1/chat/completions", s.withCommon(WithAPIKeyAuth(apiAuth, chatCompletion.ServeHTTP)))

	// Agent 内部接口（agent scope 秘钥 + 实例 ID）。
	mux.HandleFunc("GET /agent/pull", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Pull)))
	mux.HandleFunc("POST /agent/ack", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Ack)))
	mux.HandleFunc("POST /agent/health", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.Health)))
	mux.HandleFunc("GET /agent/ws", s.withCommon(WithAgentAuth(agentAuth, agentHandlers.WebSocket)))

	// 账号与会话。
	mux.HandleFunc("POST /api/auth/login", s.withCommon(authH.Login))
	mux.HandleFunc("POST /api/auth/logout", s.withCommon(WithSessionAuth(sessionAuth, authH.Logout)))
	mux.HandleFunc("GET /api/auth/me", s.withCommon(WithSessionAuth(sessionAuth, authH.Me)))
	mux.HandleFunc("POST /api/me/password", s.withCommon(WithSessionAuth(sessionAuth, usersH.ChangePassword)))

	// 用户管理（admin）。
	mux.HandleFunc("GET /api/users", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(usersH.List))))
	mux.HandleFunc("POST /api/users", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(usersH.Create))))
	mux.HandleFunc("DELETE /api/users/{id}", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(usersH.Delete))))
	mux.HandleFunc("POST /api/users/{id}/reset-password", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(usersH.ResetPassword))))
	mux.HandleFunc("GET /api/users/{id}/keys", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(keysH.ListUser))))
	mux.HandleFunc("DELETE /api/users/{id}/keys/{keyId}", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(keysH.DeleteUser))))

	// 秘钥自助管理（会话鉴权，属主隔离在用例层）。
	mux.HandleFunc("GET /api/keys", s.withCommon(WithSessionAuth(sessionAuth, keysH.ListMine)))
	mux.HandleFunc("POST /api/keys", s.withCommon(WithSessionAuth(sessionAuth, keysH.Create)))
	mux.HandleFunc("DELETE /api/keys/{id}", s.withCommon(WithSessionAuth(sessionAuth, keysH.DeleteMine)))

	// 网页控制台数据接口（会话鉴权，按租户过滤；admin 全局）。
	mux.HandleFunc("GET /api/agents", s.withCommon(WithSessionAuth(sessionAuth, console.Agents)))
	mux.HandleFunc("GET /api/tasks", s.withCommon(WithSessionAuth(sessionAuth, console.Tasks)))
	mux.HandleFunc("GET /api/models", s.withCommon(WithSessionAuth(sessionAuth, console.Models)))
	mux.HandleFunc("GET /api/overview", s.withCommon(WithSessionAuth(sessionAuth, console.Overview)))
	mux.HandleFunc("POST /api/chat", s.withCommon(WithSessionAuth(sessionAuth, chat.ServeHTTP)))

	// 机器人管理（会话鉴权）。
	if d.Bots != nil {
		bots := NewBotHandlers(d.Bots, cfg.Server.PublicAddr, d.Logger)
		mux.HandleFunc("GET /api/bots", s.withCommon(WithSessionAuth(sessionAuth, bots.List)))
		mux.HandleFunc("POST /api/bots", s.withCommon(WithSessionAuth(sessionAuth, bots.Create)))
		mux.HandleFunc("GET /api/bots/{id}", s.withCommon(WithSessionAuth(sessionAuth, bots.Get)))
		mux.HandleFunc("PUT /api/bots/{id}", s.withCommon(WithSessionAuth(sessionAuth, bots.Update)))
		mux.HandleFunc("DELETE /api/bots/{id}", s.withCommon(WithSessionAuth(sessionAuth, bots.Delete)))
		mux.HandleFunc("POST /api/bots/{id}/toggle", s.withCommon(WithSessionAuth(sessionAuth, bots.Toggle)))
	}

	// IM 机器人回调（平台调用，不经会话/秘钥鉴权；回调内按 bot 归属解析租户）。
	if d.BotInbound != nil && d.BotRepo != nil {
		hooks := NewWebhookHandlers(d.BotRepo, d.BotInbound, d.Logger)
		mux.HandleFunc("POST /webhook/feishu/{id}", s.withCommon(hooks.Feishu))
		mux.HandleFunc("GET /webhook/wecom/{id}", s.withCommon(hooks.Wecom))
		mux.HandleFunc("POST /webhook/wecom/{id}", s.withCommon(hooks.Wecom))
	}

	// 健康检查与运维接口（运维接口需 admin 会话）。
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /admin/agents", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(s.listAgents))))
	mux.HandleFunc("GET /admin/queues", s.withCommon(WithSessionAuth(sessionAuth, WithRequireAdmin(s.listQueues))))

	// 前端静态资源（SPA 托管）。
	if cfg.Web.Enabled && cfg.Web.StaticDir != "" {
		mux.Handle("/", NewSPAHandler(cfg.Web.StaticDir))
	} else {
		mux.HandleFunc("/", s.indexPlaceholder)
	}

	s.httpServer = &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return serveCtx },
	}
	return s
}

// withCommon 通用中间件：panic 恢复 + 全局在途限流 + 访问日志。
func (s *Server) withCommon(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.globalLimiter.Acquire(r.Context()); err != nil {
			writeErr(w, apperr.New(apperr.CodeRateLimited, "gateway is busy, too many in-flight requests"))
			return
		}
		defer s.globalLimiter.Release()
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered", port.F("panic", rec))
				writeErr(w, apperr.New(apperr.CodeInternal, "internal server error"))
			}
		}()
		start := time.Now()
		next(w, r)
		s.log.Debug("request done",
			port.F("method", r.Method),
			port.F("path", r.URL.Path),
			port.F("cost_ms", time.Since(start).Milliseconds()))
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
	s.serveCancel()
	return s.httpServer.Shutdown(ctx)
}

// healthz 健康检查。
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"service":  "codeporter-gateway",
		"ws_conns": connCount(s.hub),
	})
}

// listAgents 运维：列出全部实例（admin）。
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
			"owner_id":       string(a.OwnerID()),
			"name":           a.Name(),
			"status":         a.Status().String(),
			"last_heartbeat": a.LastHeartbeatAt().Unix(),
			"mcps":           a.Health().MCPs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// listQueues 运维：队列快照（admin）。
func (s *Server) listQueues(w http.ResponseWriter, _ *http.Request) {
	snapshot := map[string]int{}
	if s.queueRepo != nil {
		snapshot = s.queueRepo.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{"queues": snapshot})
}

// indexPlaceholder 未托管前端时的根路径提示。
func (s *Server) indexPlaceholder(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "codeporter",
		"docs":    "/healthz",
		"hint":    "web console is not bundled, set web.static_dir",
	})
}
