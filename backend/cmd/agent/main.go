// Command agent 启动 CodePorter 本地代理（LocalAgent）。
//
// 职责：主动出站连接网关 → 拉取/接收任务 → 本地协程池限流 → 调用本机 MCP AI 工具 → 回传结果。
// 本机不监听任何端口，外网无法主动访问，安全性由「出站连接」保证。
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

	agentapp "github.com/codeporter/code-porter/internal/application/agent"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/infrastructure/client"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
	"github.com/codeporter/code-porter/internal/infrastructure/system"
	"github.com/codeporter/code-porter/pkg/pool"
	"github.com/codeporter/code-porter/pkg/version"
)

func main() {
	configPath := flag.String("config", "configs/agent.yaml", "agent config file path")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("codeporter-agent " + version.String())
		return
	}

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "agent exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.LoadAgent(configPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "config %s not found, fallback to defaults\n", configPath)
			cfg, _ = config.LoadAgent("")
		} else {
			return err
		}
	}

	log := logging.New(os.Stdout, logging.ParseLevel(cfg.Log.Level))
	agentID := config.EnsureAgentRegistry(cfg)

	// --- 本地资源：MCP 适配层 + 硬上限协程池 ---
	mcpRegistry := mcp.NewRegistry(cfg.MCP, log)
	defer func() {
		if err := mcpRegistry.Close(); err != nil {
			log.Warn("close mcp registry failed", port.F("err", err.Error()))
		}
	}()

	workerPool := pool.New(cfg.WorkerPool.MaxConcurrency, cfg.WorkerPool.QueueSize,
		pool.WithPanicHandler(func(jobID string, recovered any) {
			log.Error("worker panic recovered", port.F("task_id", jobID), port.F("panic", recovered))
		}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerPool.Start(ctx)

	// --- 出站网关客户端 ---
	policy := agentapp.Policy{
		PullIntervalMin:   cfg.Pull.IntervalMin,
		PullIntervalMax:   cfg.Pull.IntervalMax,
		BackoffFactor:     cfg.Pull.BackoffFactor,
		MaxConcurrency:    cfg.WorkerPool.MaxConcurrency,
		QueueSize:         cfg.WorkerPool.QueueSize,
		MCPTimeout:        cfg.MCP.ClaudeCode.RequestTimeout,
		HeartbeatInterval: cfg.Health.Interval,
		ReconnectMin:      cfg.Direct.ReconnectMin,
		ReconnectMax:      cfg.Direct.ReconnectMax,
	}

	gwClient := client.NewGatewayClient(client.Config{
		BaseURL:     cfg.Gateway.Addr,
		AgentToken:  cfg.Agent.Token,
		Timeout:     cfg.Gateway.Timeout,
		InsecureTLS: cfg.Gateway.InsecureTLS,
	})

	httpReporter := agentapp.NewHTTPReporter(gwClient, string(agentID))
	executor := agentapp.NewTaskExecutor(mcpRegistry, httpReporter, log, policy)

	// --- Pull 队列模式消费者（默认开启）---
	pullConsumer := agentapp.NewPullConsumer(gwClient, agentID, workerPool, executor, httpReporter,
		port.RealClock{}, log, policy)
	go pullConsumer.Run(ctx)

	// --- SSE 直连模式消费者（可选开启）---
	if cfg.Direct.Enabled {
		factory := client.NewDirectSessionFactory(client.SessionConfig{
			BaseURL:           cfg.Gateway.Addr,
			AgentToken:        cfg.Agent.Token,
			HeartbeatInterval: cfg.Direct.HeartbeatInterval,
			PongTimeout:       cfg.Direct.PongTimeout,
			InsecureTLS:       cfg.Gateway.InsecureTLS,
		}, log)
		directConsumer := agentapp.NewDirectConsumer(factory, agentID, workerPool, executor, log, policy)
		go directConsumer.Run(ctx)
	}

	// --- 健康上报 ---
	probe := system.NewProbe()
	mcpProber := mcpProbeAdapter{registry: mcpRegistry}
	healthReporter := agentapp.NewHealthReporter(gwClient, agentID, probe, mcpProber, workerPool, log, policy)
	go healthReporter.Run(ctx)

	hostname, osName := probe.HostInfo()
	log.Info("codeporter agent started",
		port.F("version", "0.2.0"),
		port.F("agent_id", string(agentID)),
		port.F("gateway", cfg.Gateway.Addr),
		port.F("max_concurrency", cfg.WorkerPool.MaxConcurrency),
		port.F("queue_size", cfg.WorkerPool.QueueSize),
		port.F("direct_mode", cfg.Direct.Enabled),
		port.F("host", hostname),
		port.F("os", osName))

	<-ctx.Done()
	log.Info("shutting down, waiting for in-flight tasks")

	// 优雅退出：等待在途任务结束，最多等待 MCP 超时时长。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		workerPool.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		log.Warn("shutdown timeout, some tasks may be interrupted")
	}

	log.Info("codeporter agent stopped")
	return nil
}

// mcpProbeAdapter 把 MCP 注册表适配为健康探测端口。
type mcpProbeAdapter struct {
	registry *mcp.Registry
}

// Health 返回全部适配器可用性。
func (a mcpProbeAdapter) Health(ctx context.Context) []agentapp.ModelHealth {
	items := a.registry.HealthCheckAll(ctx)
	out := make([]agentapp.ModelHealth, 0, len(items))
	for _, it := range items {
		out = append(out, agentapp.ModelHealth{Model: it.Model, Available: it.Available, Detail: it.Detail})
	}
	return out
}
