package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentapp "github.com/codeporter/code-porter/internal/application/agent"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/infrastructure/client"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/feishubot"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
	"github.com/codeporter/code-porter/internal/infrastructure/system"
	"github.com/codeporter/code-porter/pkg/pool"
)

// Service 封装本地客户端的两个相互独立的运行时：
//
//	代理（agent）：出站连网关，拉取/接收网关下发的任务并执行；
//	机器人（bots）：飞书等 IM 平台长连接，消息在本机直接处理、流式回复。
//
// 二者可分别启停：不开网关也能跑机器人，不启用机器人也不影响代理。
type Service struct {
	cfgPath string
	cfg     *config.AgentConfig
	log     *logging.Logger

	// ---- 代理运行时（网关任务） ----
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	pool    *pool.Pool
	mcpReg  *mcp.Registry
	wg      sync.WaitGroup

	// ---- 机器人运行时（IM 长连接） ----
	botMu      sync.Mutex
	botRunning bool
	botCancel  context.CancelFunc
	botPool    *pool.Pool
	botReg     *mcp.Registry
	botWg      sync.WaitGroup
	botLock    *feishubot.FileLock

	// onEvent 运行时事件回调（可选），供 IPC 层转发给界面。
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
		return map[string]any{"running": false, "bots": map[string]any{}}
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
		// running/agent_running 均指「网关代理」；机器人状态见 bots。
		"running":         running,
		"agent_running":   running,
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
		"bots":            s.botsStatus(cfg),
	}
}

// botsStatus 汇总各 IM 机器人的配置与运行状态（未运行也可展示配置态）。
func (s *Service) botsStatus(cfg *config.AgentConfig) map[string]any {
	s.botMu.Lock()
	running := s.botRunning
	s.botMu.Unlock()
	fc := cfg.Bots.Feishu
	return map[string]any{
		"feishu": map[string]any{
			"running":    running,
			"enabled":    fc.Enabled,
			"configured": strings.TrimSpace(fc.AppID) != "" && strings.TrimSpace(fc.AppSecret) != "",
			"app_id":     fc.AppID,
			"model":        ifEmpty(fc.Model, string(model.ClaudeCode)),
			"mention_only": fc.MentionOnly,
		},
	}
}

func ifEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
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

// ---- 机器人运行时（与代理独立） ----

// BotsRunning 机器人服务是否运行中。
func (s *Service) BotsRunning() bool {
	s.botMu.Lock()
	defer s.botMu.Unlock()
	return s.botRunning
}

// StartBots 启动已配置的 IM 机器人（目前仅飞书）。
//
// 不依赖网关连接：机器人有独立的 MCP 注册表与协程池，消息本地闭环。
// 同一台机器上同一 App ID 只允许一个实例（文件锁），避免重复消费事件。
func (s *Service) StartBots() error {
	s.botMu.Lock()
	if s.botRunning {
		s.botMu.Unlock()
		return errors.New("机器人已在运行")
	}
	s.botMu.Unlock()

	fc := s.cfg.Bots.Feishu
	if !fc.Enabled {
		return errors.New("机器人未启用：请先在配置中开启 bots.feishu.enabled 并保存")
	}
	if strings.TrimSpace(fc.AppID) == "" || strings.TrimSpace(fc.AppSecret) == "" {
		return errors.New("bots.feishu 已启用，但 app_id / app_secret 未配置")
	}
	m := model.Model(fc.Model)
	if m == "" {
		m = model.ClaudeCode
	}
	if _, err := model.Parse(string(m)); err != nil {
		return fmt.Errorf("bots.feishu.model 非法: %w", err)
	}

	// 机器人的 MCP 配置复用独立副本（含密钥环境注入），与代理互不影响。
	botCfg := *s.cfg
	botCfg.MCP.Env = nil
	if fc2 := s.cfg.Secrets; fc2.AnthropicAPIKey != "" || fc2.OpenAIAPIKey != "" {
		env := map[string]string{}
		if fc2.AnthropicAPIKey != "" {
			env["ANTHROPIC_API_KEY"] = fc2.AnthropicAPIKey
		}
		if fc2.OpenAIAPIKey != "" {
			env["OPENAI_API_KEY"] = fc2.OpenAIAPIKey
		}
		botCfg.MCP.Env = env
	}
	registry := mcp.NewRegistry(botCfg.MCP, s.log)
	workerPool := pool.New(s.cfg.WorkerPool.MaxConcurrency, s.cfg.WorkerPool.QueueSize,
		pool.WithPanicHandler(func(jobID string, recovered any) {
			s.log.Error("bot worker panic recovered", port.F("task_id", jobID), port.F("panic", recovered))
		}))

	// 同机单实例锁：锁文件放在配置文件所在目录。
	lockDir := s.cfgPath
	if lockDir != "" {
		lockDir = filepath.Dir(lockDir)
	} else {
		lockDir = defaultClientWorkDir()
	}
	flock, err := feishubot.LockFile(lockDir, fc.AppID)
	if err != nil {
		_ = registry.Close()
		return err
	}

	policy := agentapp.Policy{
		MaxConcurrency: s.cfg.WorkerPool.MaxConcurrency,
		QueueSize:      s.cfg.WorkerPool.QueueSize,
		MCPTimeout:     s.cfg.MCP.ClaudeCode.RequestTimeout,
	}

	ctx, cancel := context.WithCancel(context.Background())
	workerPool.Start(ctx)

	// 机器人本地执行不上报网关：executor 的 reporter 实际由每个任务按流式卡片动态注入，
	// 这里传 nil 即可（ExecuteWith 要求按任务注入）。
	executor := agentapp.NewTaskExecutor(registry, nil, s.log, policy)

	svc := agentapp.NewFeishuBotService(nil, executor, workerPool, agentapp.FeishuBotConfig{
		Model:        m,
		MentionOnly:  fc.MentionOnly,
		SystemPrompt: fc.SystemPrompt,
	}, policy, s.log)
	runner, err := feishubot.NewRunner(fc.AppID, fc.AppSecret, svc.OnMessage, s.log)
	if err != nil {
		cancel()
		flock.Release()
		_ = registry.Close()
		return err
	}
	svc.SetRunner(runner)

	s.botMu.Lock()
	s.botRunning = true
	s.botCancel = cancel
	s.botPool = workerPool
	s.botReg = registry
	s.botLock = flock
	s.botMu.Unlock()

	s.botWg.Add(1)
	go func() {
		defer s.botWg.Done()
		if err := runner.Start(ctx); err != nil {
			s.log.Warn("feishu bot exited", port.F("err", err.Error()))
		}
	}()
	s.log.Info("feishu bot started",
		port.F("app_id", fc.AppID), port.F("model", m.String()),
		port.F("mention_only", fc.MentionOnly))
	s.emitEvent("status", s.Status())
	return nil
}

// StopBots 停止全部 IM 机器人并释放其独立运行时。
func (s *Service) StopBots() {
	s.botMu.Lock()
	if !s.botRunning {
		s.botMu.Unlock()
		return
	}
	s.botRunning = false
	cancel := s.botCancel
	workerPool := s.botPool
	registry := s.botReg
	flock := s.botLock
	s.botCancel, s.botPool, s.botReg, s.botLock = nil, nil, nil, nil
	s.botMu.Unlock()

	if cancel != nil {
		cancel()
	}
	shutdownCtx, c := context.WithTimeout(context.Background(), 20*time.Second)
	defer c()
	done := make(chan struct{})
	go func() {
		if workerPool != nil {
			workerPool.Stop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		s.log.Warn("bots shutdown timeout, some tasks may be interrupted")
	}
	s.botWg.Wait()
	if registry != nil {
		if err := registry.Close(); err != nil {
			s.log.Warn("close bot mcp registry failed", port.F("err", err.Error()))
		}
	}
	if flock != nil {
		flock.Release()
	}
	s.log.Info("feishu bot stopped")
	s.emitEvent("status", s.Status())
}

// BotTestResult 机器人连接测试结果。
type BotTestResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Tenant  string `json:"tenant_key,omitempty"`
	Expire  int    `json:"expire_seconds,omitempty"`
}

// TestBot 不启动长连接，仅验证当前配置的机器人凭证是否可用。
func (s *Service) TestBot(channel string) (BotTestResult, error) {
	fc := s.cfg.Bots.Feishu
	if strings.TrimSpace(fc.AppID) == "" || strings.TrimSpace(fc.AppSecret) == "" {
		return BotTestResult{Channel: "feishu", Detail: "未配置 App ID / App Secret"}, errors.New("feishu app credentials missing")
	}
	runner, err := feishubot.NewRunner(fc.AppID, fc.AppSecret,
		func(context.Context, port.IMBotMessage) error { return nil }, s.log)
	if err != nil {
		return BotTestResult{Channel: "feishu", Detail: err.Error()}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := runner.TestCredentials(ctx)
	if err != nil {
		return BotTestResult{Channel: "feishu", OK: false, Detail: r.Detail}, err
	}
	return BotTestResult{
		Channel: "feishu", OK: true, Detail: r.Detail,
		Tenant: r.TenantKey, Expire: r.ExpireSeconds,
	}, nil
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
