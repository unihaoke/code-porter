// Command gateway 启动 CodePorter 公网网关服务。
//
// 职责：多租户账号/秘钥鉴权 → 接收 OpenAI 兼容请求 → 按通路（Pull 队列 / SSE 直连）
// 下发任务到属主自己的本地客户端 → 汇聚输出回传调用方。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	gatewayapp "github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	mysqlpersist "github.com/codeporter/code-porter/internal/infrastructure/persistence/mysql"
	"github.com/codeporter/code-porter/internal/infrastructure/security"
	gwhttp "github.com/codeporter/code-porter/internal/infrastructure/transport/http"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
	"github.com/codeporter/code-porter/pkg/version"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	configPath := flag.String("config", "configs/gateway.yaml", "gateway config file path")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("codeporter-gateway " + version.String())
		return
	}

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "gateway exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.LoadGateway(configPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "config %s not found, fallback to defaults\n", configPath)
			cfg, _ = config.LoadGateway("")
		} else {
			return err
		}
	}

	log := logging.New(os.Stdout, logging.ParseLevel(cfg.Log.Level))

	// 旧配置/环境变量只告警，绝不回退生效。
	for _, msg := range cfg.LegacyWarnings() {
		log.Warn("deprecated config: " + msg)
	}
	for _, msg := range config.DeprecatedEnvWarnings(configPath) {
		log.Warn(msg)
	}

	if cfg.Database.DSN == "" {
		return errors.New("database.dsn 未配置：请在 configs/gateway.yaml 配置 database.dsn 或设置 MYSQL_DSN 环境变量")
	}

	// --- 基础设施（Driven Adapters）---
	// 运行时态仍在内存（任务队列/事件 broker/在线状态），账号体系在 MySQL。
	taskRepo := memory.NewTaskRepository()
	queueRepo := memory.NewTaskQueueRepository()
	eventBroker := broker.NewMemoryBroker()
	clock := port.RealClock{}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 60*time.Second)
	db, err := mysqlpersist.Open(startupCtx, cfg.Database.DSN, mysqlpersist.PoolConfig{
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
	})
	cancelStartup()
	if err != nil {
		return fmt.Errorf("connect mysql: %w", err)
	}
	defer db.Close()

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelMigrate()
	if err := mysqlpersist.Migrate(migrateCtx, cfg.Database.DSN, db); err != nil {
		return fmt.Errorf("mysql migrate: %w", err)
	}

	userRepo := mysqlpersist.NewUserRepository(db)
	keyRepo := mysqlpersist.NewAPIKeyRepository(db)
	sessionRepo := mysqlpersist.NewSessionRepository(db)
	// agents 表只持久化身份；在线状态/健康快照等运行时态由内存装饰器在网关
	// 进程内维护（重启后随 Agent 下一次 pull/健康上报重建），不能直接把
	// MySQL 仓储交给 registry——否则每次读回都是 offline 副本。
	agentRepo := memory.NewRuntimeAgentRepository(mysqlpersist.NewAgentRepository(db))

	// --- 应用服务（用例编排）---
	policy := gatewayapp.TaskPolicy{
		MaxRetry:          cfg.Task.MaxRetry,
		LockTimeout:       cfg.Task.LockTimeout,
		TTL:               cfg.Task.TTL,
		QueueMaxLen:       cfg.Task.QueueMaxLen,
		RequestTimeout:    cfg.Task.RequestTimeout,
		StreamIdleTimeout: cfg.Task.StreamIdleTimeout,
		MCPTimeout:        cfg.Task.MCPTimeout,
	}

	registry := gatewayapp.NewAgentRegistry(agentRepo, queueRepo, clock, log, policy)

	ackUC := gatewayapp.NewAckTaskUseCase(taskRepo, queueRepo, registry, eventBroker, clock, log, policy)
	healthUC := gatewayapp.NewReportHealthUseCase(registry, clock, log, policy)
	pullUC := gatewayapp.NewPullTasksUseCase(taskRepo, queueRepo, registry, clock, log, policy)

	hub := ws.NewHub(ws.HubConfig{PingInterval: 30 * time.Second}, func(ctx context.Context, req port.AckRequest) error {
		_, err := ackUC.Execute(ctx, gatewayapp.AckCommand{
			TaskID:    req.TaskID,
			AgentID:   agent.ID(req.AgentID),
			LockToken: req.LockToken,
			Status:    req.Status,
			Chunks:    req.Chunks,
			Error:     req.Error,
			Result:    req.Result,
		})
		return err
	}, log)

	submitUC := gatewayapp.NewSubmitTaskUseCase(taskRepo, queueRepo, registry, eventBroker, hub, clock, log, policy)
	defaultModel := model.Model(cfg.Chat.DefaultModel)
	chatUC := gatewayapp.NewChatUseCase(submitUC, defaultModel, log)

	authSvc := authsvc.NewService(authsvc.Deps{
		Users:      userRepo,
		Keys:       keyRepo,
		Sessions:   sessionRepo,
		Hasher:     security.NewBcryptHasher(),
		Secrets:    security.NewSHA256Hasher(),
		Generator:  security.NewRandomGenerator(),
		Clock:      clock,
		Logger:     log,
		SessionTTL: cfg.Auth.SessionTTL,
		AfterUserDelete: func(ctx context.Context, deletedID user.ID) {
			ids, err := registry.EvictOwner(ctx, deletedID)
			if err != nil {
				log.Warn("evict owner failed", port.F("user_id", string(deletedID)), port.F("err", err.Error()))
			}
			for _, id := range ids {
				hub.Disconnect(string(id))
			}
		},
	})

	// 默认密码告警：种子 admin 仍是 admin123 时显著提示。
	if authSvc.IsUsingDefaultPassword(context.Background()) {
		log.Warn("默认管理员仍在使用默认密码 admin/admin123，请立即登录后在个人菜单修改密码")
	}

	lifecycle := gatewayapp.NewTaskLifecycleService(taskRepo, queueRepo, registry, eventBroker, clock, log, policy,
		gatewayapp.LifecycleConfig{
			LockSweepInterval:      cfg.Lifecycle.LockSweepInterval,
			TTLSweepInterval:       cfg.Lifecycle.TTLSweepInterval,
			HeartbeatSweepInterval: cfg.Lifecycle.HeartbeatSweepInterval,
			RetentionInterval:      cfg.Lifecycle.RetentionInterval,
			TaskRetention:          cfg.Lifecycle.TaskRetention,
		})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lifecycle.Start(ctx)

	server := gwhttp.NewServer(gwhttp.Deps{
		Config:    cfg,
		Auth:      authSvc,
		Registry:  registry,
		Submit:    submitUC,
		Chat:      chatUC,
		Pull:      pullUC,
		Ack:       ackUC,
		Health:    healthUC,
		Hub:       hub,
		QueueRepo: queueRepo,
		TaskRepo:  taskRepo,
		Users:     userRepo,
		Logger:    log,
		Policy:    policy,
	})

	log.Info("codeporter gateway starting",
		port.F("version", version.Get().Version),
		port.F("addr", cfg.Server.Addr),
		port.F("web", cfg.Web.Enabled && cfg.Web.StaticDir != ""),
		port.F("session_ttl", cfg.Auth.SessionTTL.String()),
		port.F("queue_max_len", policy.QueueMaxLen))

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("codeporter gateway stopped")
	return nil
}
