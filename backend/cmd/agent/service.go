package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	agentapp "github.com/codeporter/code-porter/internal/application/agent"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/infrastructure/client"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
	"github.com/codeporter/code-porter/internal/infrastructure/system"
	"github.com/codeporter/code-porter/pkg/pool"
)

// Service 封装 LocalAgent 的启动与停止，命令行与 Windows GUI 共用同一套运行逻辑。
type Service struct {
	cfgPath string
	cfg     *config.AgentConfig
	log     *logging.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	pool    *pool.Pool
	mcpReg  *mcp.Registry
	wg      sync.WaitGroup

	// onEvent 运行时事件回调（可选），供 IPC / GUI 层转发给界面。
	onEvent func(kind string, data any)
}

// SetEventHook 注册运行时事件回调。应在 Start 之前调用。
// kind 取值：status / health / task。
func (s *Service) SetEventHook(fn func(kind string, data any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEvent = fn
}

// emitEvent 触发事件回调；回调自身的问题不会影响代理运行。
func (s *Service) emitEvent(kind string, data any) {
	s.mu.Lock()
	fn := s.onEvent
	s.mu.Unlock()
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	go fn(kind, data)
}

// NewService 构造服务实例。cfg 为已加载配置；log 接收运行日志（GUI 可替换为自定义 writer）。
func NewService(cfgPath string, cfg *config.AgentConfig, log *logging.Logger) *Service {
	return &Service{cfgPath: cfgPath, cfg: cfg, log: log}
}

// Running 报告代理是否正在运行。
func (s *Service) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Status 返回当前运行状态快照，供界面展示。
func (s *Service) Status() map[string]any {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	cfg := s.cfg
	if cfg == nil {
		return map[string]any{"running": running}
	}
	aiTools := make([]map[string]any, 0, len(model.All()))
	for _, m := range model.All() {
		acfg := cfg.MCP.For(m)
		label := m.String()
		if acfg.Command != "" {
			label = acfg.Command
		}
		aiTools = append(aiTools, map[string]any{
			"model":   m.String(),
			"label":   label,
			"enabled": acfg.Enabled,
			"mode":    string(acfg.Mode),
			"command": acfg.Command,
		})
	}
	workDir := strings.TrimSpace(cfg.MCP.WorkDir)
	if workDir == "" {
		workDir = defaultClientWorkDir()
	}
	return map[string]any{
		"running":         running,
		"version":         "0.2.0",
		"agent_id":        cfg.Agent.ID,
		"gateway":         cfg.Gateway.Addr,
		"log_level":       cfg.Log.Level,
		"max_concurrency": cfg.WorkerPool.MaxConcurrency,
		"queue_size":      cfg.WorkerPool.QueueSize,
		"direct_mode":     cfg.Direct.Enabled,
		"work_dir":        workDir,
		"config_path":     s.cfgPath,
		"ai_tools":        aiTools,
	}
}

// Start 启动 Agent（非阻塞）。启动所有消费者协程后立刻返回。
func (s *Service) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("agent 已在运行")
	}
	s.mu.Unlock()

	cfg := s.cfg
	instanceID, err := config.EnsureAgentIdentity(cfg, s.cfgPath)
	if err != nil {
		return err
	}

	// 把 AI 密钥注入到 MCP 子进程环境变量（如 ANTHROPIC_API_KEY），供本机 AI 工具使用。
	if cfg.Secrets.AnthropicAPIKey != "" || cfg.Secrets.OpenAIAPIKey != "" {
		env := map[string]string{}
		if cfg.Secrets.AnthropicAPIKey != "" {
			env["ANTHROPIC_API_KEY"] = cfg.Secrets.AnthropicAPIKey
		}
		if cfg.Secrets.OpenAIAPIKey != "" {
			env["OPENAI_API_KEY"] = cfg.Secrets.OpenAIAPIKey
		}
		cfg.MCP.Env = env
	}

	mcpRegistry := mcp.NewRegistry(cfg.MCP, s.log)
	s.mcpReg = mcpRegistry

	workerPool := pool.New(cfg.WorkerPool.MaxConcurrency, cfg.WorkerPool.QueueSize,
		pool.WithPanicHandler(func(jobID string, recovered any) {
			s.log.Error("worker panic recovered", port.F("task_id", jobID), port.F("panic", recovered))
		}))
	s.pool = workerPool

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	workerPool.Start(ctx)

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
		Key:         cfg.Agent.Key,
		InstanceID:  instanceID,
		Name:        cfg.Agent.Name,
		Timeout:     cfg.Gateway.Timeout,
		InsecureTLS: cfg.Gateway.InsecureTLS,
	})

	httpReporter := agentapp.NewHTTPReporter(gwClient, instanceID)
	executor := agentapp.NewTaskExecutor(mcpRegistry, httpReporter, s.log, policy)
	executor.OnEvent = func(ev agentapp.TaskEvent) {
		s.emitEvent("task", map[string]any{
			"task_id":    ev.TaskID,
			"model":      ev.Model,
			"phase":      ev.Phase,
			"elapsed_ms": ev.Elapsed.Milliseconds(),
			"detail":     ev.Detail,
		})
	}

	// Pull 队列模式消费者（默认开启）
	pullConsumer := agentapp.NewPullConsumer(gwClient, agent.ID(instanceID), workerPool, executor, httpReporter,
		port.RealClock{}, s.log, policy)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		pullConsumer.Run(ctx)
	}()

	// SSE 直连模式消费者（可选开启）
	if cfg.Direct.Enabled {
		factory := client.NewDirectSessionFactory(client.SessionConfig{
			BaseURL:           cfg.Gateway.Addr,
			Key:               cfg.Agent.Key,
			InstanceID:        instanceID,
			Name:              cfg.Agent.Name,
			HeartbeatInterval: cfg.Direct.HeartbeatInterval,
			PongTimeout:       cfg.Direct.PongTimeout,
			InsecureTLS:       cfg.Gateway.InsecureTLS,
		}, s.log)
		directConsumer := agentapp.NewDirectConsumer(factory, agent.ID(instanceID), workerPool, executor, s.log, policy)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			directConsumer.Run(ctx)
		}()
	}

	// 健康上报
	probe := system.NewProbe()
	mcpProber := mcpProbeAdapter{registry: mcpRegistry, onEvent: s.emitEvent}
	healthReporter := agentapp.NewHealthReporter(gwClient, agent.ID(instanceID), probe, mcpProber, workerPool, s.log, policy)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		healthReporter.Run(ctx)
	}()

	hostname, osName := probe.HostInfo()
	s.log.Info("codeporter agent started",
		port.F("version", "0.2.0"),
		port.F("agent_id", instanceID),
		port.F("agent_name", cfg.Agent.Name),
		port.F("gateway", cfg.Gateway.Addr),
		port.F("max_concurrency", cfg.WorkerPool.MaxConcurrency),
		port.F("queue_size", cfg.WorkerPool.QueueSize),
		port.F("direct_mode", cfg.Direct.Enabled),
		port.F("host", hostname),
		port.F("os", osName))

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	s.emitEvent("status", s.Status())
	return nil
}

// Stop 优雅停止 Agent：取消上下文、等待在途任务结束（最多 30s）、关闭 MCP 注册表。
func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		if s.pool != nil {
			s.pool.Stop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		s.log.Warn("shutdown timeout, some tasks may be interrupted")
	}

	s.wg.Wait()

	if s.mcpReg != nil {
		if err := s.mcpReg.Close(); err != nil {
			s.log.Warn("close mcp registry failed", port.F("err", err.Error()))
		}
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	s.log.Info("codeporter agent stopped")
	s.emitEvent("status", s.Status())
}

// mcpProbeAdapter 把 MCP 注册表适配为健康探测端口。
type mcpProbeAdapter struct {
	registry *mcp.Registry
	// onEvent 可选：把探测结果同时推给界面。
	onEvent func(kind string, data any)
}

// Health 返回全部适配器可用性。
func (a mcpProbeAdapter) Health(ctx context.Context) []agentapp.ModelHealth {
	items := a.registry.HealthCheckAll(ctx)
	out := make([]agentapp.ModelHealth, 0, len(items))
	for _, it := range items {
		out = append(out, agentapp.ModelHealth{Model: it.Model, Available: it.Available, Detail: it.Detail})
	}
	if a.onEvent != nil {
		payload := make([]map[string]any, 0, len(items))
		for _, it := range items {
			payload = append(payload, map[string]any{
				"model": string(it.Model), "available": it.Available, "detail": it.Detail,
			})
		}
		a.onEvent("health", payload)
	}
	return out
}
