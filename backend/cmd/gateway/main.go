// Command gateway 启动 CodePorter 公网网关服务。
//
// 职责：接收外部 OpenAI 兼容请求 → 按通路（Pull 队列 / SSE 直连）下发任务 →
// 汇聚本地 AI 输出并以 SSE 或 JSON 形式回传给调用方。
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

	gatewayapp "github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	infrabot "github.com/codeporter/code-porter/internal/infrastructure/bot"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	gwhttp "github.com/codeporter/code-porter/internal/infrastructure/transport/http"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
	"github.com/codeporter/code-porter/pkg/version"
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

	// --- 基础设施（Driven Adapters）---
	taskRepo := memory.NewTaskRepository()
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	eventBroker := broker.NewMemoryBroker()
	clock := port.RealClock{}

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

	registry := gatewayapp.NewAgentRegistry(agentRepo, queueRepo, clock, log,
		agent.ID(cfg.Agent.ID), policy)

	defaultAgentID := agent.ID(cfg.Agent.ID)
	token := cfg.Agent.Token
	if token == "" && len(cfg.Security.AgentTokens) > 0 {
		token = cfg.Security.AgentTokens[0]
	}
	if _, err := registry.Ensure(context.Background(), defaultAgentID, cfg.Agent.Name, token); err != nil {
		return fmt.Errorf("bootstrap default agent: %w", err)
	}

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

	// --- IM 机器人（飞书 / 企业微信）---
	botRepo, err := infrabot.NewFileBotRepository(cfg.Bot.StoreFile)
	if err != nil {
		return fmt.Errorf("init bot repository: %w", err)
	}
	botAdminUC := gatewayapp.NewBotAdminUseCase(botRepo, clock, log)
	defaultModel := model.Model(cfg.Bot.DefaultModel)
	botInboundUC := gatewayapp.NewBotInboundUseCase(
		botRepo, submitUC, infrabot.NewSender(10*time.Second, log),
		defaultModel, clock, log, policy)
	chatUC := gatewayapp.NewChatUseCase(submitUC, defaultModel, log)

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
		Config:     cfg,
		Registry:   registry,
		Submit:     submitUC,
		Pull:       pullUC,
		Ack:        ackUC,
		Health:     healthUC,
		Hub:        hub,
		QueueRepo:  queueRepo,
		TaskRepo:   taskRepo,
		Chat:       chatUC,
		Bots:       botAdminUC,
		BotInbound: botInboundUC,
		BotRepo:    botRepo,
		Logger:     log,
		Policy:     policy,
	})

	log.Info("codeporter gateway starting",
		port.F("version", version.Get().Version),
		port.F("addr", cfg.Server.Addr),
		port.F("agent", string(defaultAgentID)),
		port.F("web", cfg.Web.Enabled && cfg.Web.StaticDir != ""),
		port.F("queue_max_len", policy.QueueMaxLen))

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("codeporter gateway stopped")
	return nil
}
