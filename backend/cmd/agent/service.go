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
	"github.com/codeporter/code-porter/internal/infrastructure/imbot"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
	"github.com/codeporter/code-porter/internal/infrastructure/system"
	"github.com/codeporter/code-porter/pkg/lockfile"
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

	// ---- 机器人运行时（IM 长连接，按渠道独立） ----
	// 每个渠道（feishu/wecom/…）有独立的连接、协程池、MCP 注册表与单实例锁，
	// 可分别启停；bots 以渠道名为键。
	botsMu sync.Mutex
	bots   map[string]*botRuntime

	// ---- 本地 AI 工具运行时（手动预热） ----
	//
	// 与代理 / 机器人都独立：只负责把配置中已启用的 MCP 子进程提前拉起，
	// 不连网关、不连 IM。客户端启动时不会自动创建，仅由界面按钮显式启停。
	toolsMu      sync.Mutex
	toolsRunning bool
	toolsReg     *mcp.Registry

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
	return &Service{cfgPath: cfgPath, cfg: cfg, log: log, bots: make(map[string]*botRuntime)}
}

// UpdateConfig 热更新内存配置指针：IPC 层 config.save 落盘后同步调用。
//
// 注意语义边界：已经在运行的实例（代理消费者、各渠道机器人、工具预热注册表）
// 在启动时就固化了各自的配置副本/子进程环境，不会因本次替换而改变，需按界面
// 「保存并重启」语义 stop+start 才生效；但状态展示（Status）以及之后的
// Start / StartBot / StartTools / TestBot 立即按新配置执行。
func (s *Service) UpdateConfig(cfg *config.AgentConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// getConfig 返回当前配置指针（配置可能被 UpdateConfig 热替换，必须经锁读取）。
func (s *Service) getConfig() *config.AgentConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
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
	s.toolsMu.Lock()
	toolsRunning := s.toolsRunning
	toolsReg := s.toolsReg
	s.toolsMu.Unlock()
	var warmed map[model.Model]bool
	if toolsRunning && toolsReg != nil {
		warmed = toolsReg.RunningModels()
	}
	cfg := s.getConfig()
	if cfg == nil {
		return map[string]any{"running": false, "tools_running": false, "bots": map[string]any{}}
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
			"running": warmed[m],
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
		"tools_running":   toolsRunning,
		"bots":            s.botsStatus(cfg),
	}
}

// botsStatus 汇总各 IM 渠道的配置与运行状态（未运行也可展示配置态）。
// 注意：绝不回传 secret，只回传是否已配置与脱敏的身份标识。
func (s *Service) botsStatus(cfg *config.AgentConfig) map[string]any {
	s.botsMu.Lock()
	runtimes := make(map[string]*botRuntime, len(s.bots))
	for k, v := range s.bots {
		runtimes[k] = v
	}
	s.botsMu.Unlock()

	out := make(map[string]any, len(imbot.Channels()))
	for _, ch := range imbot.Channels() {
		out[ch] = botStatusItem(cfg, ch, runtimes[ch])
	}
	return out
}

func botStatusItem(cfg *config.AgentConfig, channel string, rt *botRuntime) map[string]any {
	bc := channelBotConfig(cfg, channel)
	item := map[string]any{
		"running":       rt != nil,
		"enabled":       bc.enabled,
		"configured":    strings.TrimSpace(bc.credID) != "" && strings.TrimSpace(bc.credSecret) != "",
		"credential_id": bc.credID, // 统一键
		"model":         ifEmpty(bc.model, string(model.ClaudeCode)),
		"mention_only":  bc.mentionOnly,
	}
	// 渠道历史键（前端兼容/展示），不含任何 secret。
	switch channel {
	case imbot.ChannelFeishu:
		item["app_id"] = bc.credID
	case imbot.ChannelWeCom:
		item["bot_id"] = bc.credID
	}
	return item
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

	cfg := s.getConfig()
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

// ---- 机器人运行时（与代理独立，按渠道独立启停） ----

// botRuntime 单个 IM 渠道的运行时资源：渠道之间互不共享池/注册表/锁。
type botRuntime struct {
	channel string
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	pool    *pool.Pool
	reg     *mcp.Registry
	lock    *lockfile.Lock
	runner  port.IMBotRunner
	svc     *agentapp.IMBotService
}

// channelSettings 某渠道配置的平台无关投影。
type channelSettings struct {
	enabled      bool
	credID       string
	credSecret   string
	model        string
	mentionOnly  bool
	systemPrompt string
}

func channelBotConfig(cfg *config.AgentConfig, channel string) channelSettings {
	switch channel {
	case imbot.ChannelFeishu:
		fc := cfg.Bots.Feishu
		return channelSettings{
			enabled: fc.Enabled, credID: fc.AppID, credSecret: fc.AppSecret,
			model: fc.Model, mentionOnly: fc.MentionOnly, systemPrompt: fc.SystemPrompt,
		}
	case imbot.ChannelWeCom:
		wc := cfg.Bots.WeCom
		return channelSettings{
			enabled: wc.Enabled, credID: wc.BotID, credSecret: wc.Secret,
			model: wc.Model, mentionOnly: wc.MentionOnly, systemPrompt: wc.SystemPrompt,
		}
	default:
		return channelSettings{}
	}
}

// BotsRunning 是否有任意渠道机器人在运行。
func (s *Service) BotsRunning() bool {
	s.botsMu.Lock()
	defer s.botsMu.Unlock()
	return len(s.bots) > 0
}

// BotChannelRunning 指定渠道是否在运行。
func (s *Service) BotChannelRunning(channel string) bool {
	s.botsMu.Lock()
	defer s.botsMu.Unlock()
	_, ok := s.bots[channel]
	return ok
}

// botLockDir 返回单实例锁文件目录（配置文件所在目录，兜底工作目录）。
func (s *Service) botLockDir() string {
	if strings.TrimSpace(s.cfgPath) != "" {
		return filepath.Dir(s.cfgPath)
	}
	return defaultClientWorkDir()
}

// StartEnabledBots 启动配置中所有 enabled 渠道；单个渠道失败只告警，不影响其他渠道。
// 供无界面启动（main）使用；GUI 按渠道调用 StartBot。
func (s *Service) StartEnabledBots() {
	for _, channel := range imbot.Channels() {
		settings := channelBotConfig(s.getConfig(), channel)
		if !settings.enabled {
			continue
		}
		if err := s.StartBot(channel); err != nil {
			s.log.Warn("im bot not started",
				port.F("channel", channel), port.F("err", err.Error()))
		}
	}
}

// ChannelStartResult 聚合启动时单个渠道的结果（逐渠道回执失败原因）。
type ChannelStartResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
}

// StartBots 启动全部已启用渠道；逐渠道返回结果，单个渠道失败不阻断其他渠道。
//   - 没有任何渠道启用：返回错误；
//   - 全部失败：返回错误（含每个渠道的原因）；
//   - 部分失败：结果与错误同时返回，调用方应认为成功渠道已在运行；
//   - 全部成功：错误为 nil。
func (s *Service) StartBots() ([]ChannelStartResult, error) {
	var results []ChannelStartResult
	var failed []string
	enabled := 0
	for _, channel := range imbot.Channels() {
		if !channelBotConfig(s.getConfig(), channel).enabled {
			continue
		}
		enabled++
		// 已在运行视为成功（聚合入口可能被重复调用）。
		if s.BotChannelRunning(channel) {
			results = append(results, ChannelStartResult{Channel: channel, OK: true})
			continue
		}
		if err := s.StartBot(channel); err != nil {
			results = append(results, ChannelStartResult{Channel: channel, OK: false, Detail: err.Error()})
			failed = append(failed, channel+": "+err.Error())
			continue
		}
		results = append(results, ChannelStartResult{Channel: channel, OK: true})
	}
	if enabled == 0 {
		return nil, errors.New("未启用任何 IM 机器人：请先在配置中开启 bots.feishu.enabled 或 bots.wecom.enabled 并保存")
	}
	if len(failed) > 0 {
		msg := strings.Join(failed, "；")
		if len(failed) == enabled {
			return results, errors.New(msg)
		}
		return results, errors.New("部分渠道启动失败（其余渠道已正常运行）：" + msg)
	}
	return results, nil
}

// StartBot 启动指定渠道的机器人。
//
// 不依赖网关连接：每个渠道有独立的 MCP 注册表与协程池，消息本地闭环。
// 同一台机器上同一渠道同一身份只允许一个实例（pkg/lockfile 心跳锁），
// 不同渠道/不同身份互不阻塞。
func (s *Service) StartBot(channel string) error {
	if !imbot.IsSupported(channel) {
		return fmt.Errorf("未知 IM 渠道: %q（支持: %v）", channel, imbot.Channels())
	}
	settings := channelBotConfig(s.getConfig(), channel)
	if !settings.enabled {
		return fmt.Errorf("%s 机器人未启用：请先在配置中开启 bots.%s.enabled 并保存", channel, channel)
	}
	if strings.TrimSpace(settings.credID) == "" || strings.TrimSpace(settings.credSecret) == "" {
		return fmt.Errorf("bots.%s 已启用，但凭证未配置", channel)
	}
	mdl := model.Model(settings.model)
	if mdl == "" {
		mdl = model.ClaudeCode
	}
	if _, err := model.Parse(string(mdl)); err != nil {
		return fmt.Errorf("bots.%s.model 非法: %w", channel, err)
	}

	s.botsMu.Lock()
	if _, exists := s.bots[channel]; exists {
		s.botsMu.Unlock()
		return fmt.Errorf("%s 机器人已在运行", channel)
	}
	s.botsMu.Unlock()

	// 1) 单实例锁（fail-fast：拿不到锁就不创建任何资源）。
	flock, err := lockfile.Acquire(s.botLockDir(), channel, settings.credID)
	if err != nil {
		return err
	}

	// 2) 该渠道独立的 MCP 注册表（含密钥环境注入），与代理/其他渠道互不影响。
	cfg := s.getConfig()
	botCfg := *cfg
	botCfg.MCP.Env = s.secretsEnv(cfg)
	registry := mcp.NewRegistry(botCfg.MCP, s.log)

	policy := agentapp.Policy{
		MaxConcurrency: cfg.WorkerPool.MaxConcurrency,
		QueueSize:      cfg.WorkerPool.QueueSize,
		MCPTimeout:     cfg.MCP.ClaudeCode.RequestTimeout,
	}
	ctx, cancel := context.WithCancel(context.Background())
	workerPool := pool.New(cfg.WorkerPool.MaxConcurrency, cfg.WorkerPool.QueueSize,
		pool.WithPanicHandler(func(jobID string, recovered any) {
			s.log.Error("bot worker panic recovered",
				port.F("channel", channel), port.F("task_id", jobID), port.F("panic", recovered))
		}))
	workerPool.Start(ctx)

	// 3) 平台无关的应用层服务 + 工厂构造渠道 runner（策略模式）。
	// 机器人本地执行不上报网关：executor 的 reporter 由每个任务按流式卡片动态注入。
	executor := agentapp.NewTaskExecutor(registry, nil, s.log, policy)
	svc := agentapp.NewIMBotService(nil, executor, workerPool, agentapp.IMBotServiceConfig{
		Channel:      channel,
		Model:        mdl,
		MentionOnly:  settings.mentionOnly,
		SystemPrompt: settings.systemPrompt,
	}, policy, s.log)
	creds := imbot.Credentials{}
	switch channel {
	case imbot.ChannelFeishu:
		creds = imbot.Credentials{AppID: settings.credID, AppSecret: settings.credSecret}
	case imbot.ChannelWeCom:
		creds = imbot.Credentials{BotID: settings.credID, BotSecret: settings.credSecret}
	}
	runner, err := imbot.New(imbot.Spec{
		Channel: channel, Creds: creds, OnMessage: svc.OnMessage, Log: s.log,
	})
	if err != nil {
		cancel()
		workerPool.Stop()
		_ = registry.Close()
		flock.Release()
		return err
	}
	svc.SetRunner(runner)

	rt := &botRuntime{
		channel: channel, cancel: cancel, pool: workerPool,
		reg: registry, lock: flock, runner: runner, svc: svc,
	}
	s.botsMu.Lock()
	if _, exists := s.bots[channel]; exists {
		// 并发 StartBot 竞态兜底。
		s.botsMu.Unlock()
		cancel()
		workerPool.Stop()
		_ = registry.Close()
		flock.Release()
		return fmt.Errorf("%s 机器人已在运行", channel)
	}
	s.bots[channel] = rt
	s.botsMu.Unlock()

	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		err := runner.Start(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("im bot exited", port.F("channel", channel), port.F("err", err.Error()))
		}
	}()
	s.log.Info("im bot started",
		port.F("channel", channel), port.F("credential_id", settings.credID),
		port.F("model", mdl.String()), port.F("mention_only", settings.mentionOnly))
	s.emitEvent("status", s.Status())
	return nil
}

// StopBots 停止全部渠道（幂等）。
func (s *Service) StopBots() {
	for _, channel := range imbot.Channels() {
		s.StopBot(channel)
	}
}

// StopBot 停止指定渠道并释放其独立运行时；返回它是否原本在运行。
func (s *Service) StopBot(channel string) bool {
	s.botsMu.Lock()
	rt := s.bots[channel]
	delete(s.bots, channel)
	s.botsMu.Unlock()
	if rt == nil {
		return false
	}

	rt.cancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		rt.pool.Stop()
		close(done)
	}()
	poolStopped := false
	select {
	case <-done:
		poolStopped = true
	case <-shutdownCtx.Done():
		s.log.Warn("bot shutdown timeout, some tasks may still be running",
			port.F("channel", channel))
	}
	rt.wg.Wait()
	if err := rt.reg.Close(); err != nil {
		s.log.Warn("close bot mcp registry failed",
			port.F("channel", channel), port.F("err", err.Error()))
	}
	if poolStopped {
		rt.lock.Release()
	} else {
		// 仍有任务未结束：不能立即删锁，否则另一个实例会马上抢锁，与本进程
		// 残留任务形成短暂双连接。改为放弃心跳，让锁在 staleAfter 后自然失效。
		rt.lock.Abandon()
		s.log.Warn("bot lock abandoned; it will expire after the stale window",
			port.F("channel", channel), port.F("stale_after", "20s"))
	}
	s.log.Info("im bot stopped", port.F("channel", channel))
	s.emitEvent("status", s.Status())
	return true
}

// ---- 本地 AI 工具运行时（与代理 / 机器人独立，手动启停） ----

// ToolStartResult 单个本地 AI 工具的启动（预热）结果。
type ToolStartResult struct {
	Model  string `json:"model"`
	Label  string `json:"label"`
	Mode   string `json:"mode"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ToolsRunning 本地 AI 工具运行时是否处于启动状态。
func (s *Service) ToolsRunning() bool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	return s.toolsRunning
}

// StartTools 启动（预热）配置中已启用的本地 AI 工具。
//
// 仅显式调用：客户端开机不会自动执行。MCP 模式工具的常驻子进程会被立即
// 拉起并完成握手；CLI 模式工具无常驻进程，只做免额度的可执行性探测。
// 单个工具失败不影响其他工具；全部失败时返回错误并释放注册表。
func (s *Service) StartTools() ([]ToolStartResult, error) {
	s.toolsMu.Lock()
	if s.toolsRunning {
		s.toolsMu.Unlock()
		return nil, errors.New("本地 AI 工具已在运行")
	}
	s.toolsMu.Unlock()

	cfg := s.getConfig()
	enabled := make([]model.Model, 0, len(model.All()))
	for _, m := range model.All() {
		if cfg.MCP.For(m).Enabled {
			enabled = append(enabled, m)
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("没有已启用的本地 AI 工具：请先在配置中启用至少一个工具并保存")
	}

	// 使用独立配置副本与独立注册表，预热出的子进程不受代理 / 机器人
	// 启停影响；密钥环境注入与代理启动时保持一致。
	local := *cfg
	local.MCP.Env = s.secretsEnv(cfg)
	registry := mcp.NewRegistry(local.MCP, s.log)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	warmups := registry.WarmupEnabled(ctx)

	results := make([]ToolStartResult, 0, len(warmups))
	okCount := 0
	for _, w := range warmups {
		acfg := cfg.MCP.For(w.Model)
		label := w.Model.String()
		if acfg.Command != "" {
			label = acfg.Command
		}
		mode := string(w.Mode)
		if mode == "" {
			mode = string(mcp.ModeMCP)
		}
		if w.OK {
			okCount++
		}
		results = append(results, ToolStartResult{
			Model: w.Model.String(), Label: label, Mode: mode,
			OK: w.OK, Detail: w.Detail,
		})
		s.log.Info("local ai tool warmup",
			port.F("model", w.Model.String()), port.F("mode", mode),
			port.F("ok", w.OK), port.F("detail", w.Detail))
	}
	if okCount == 0 {
		_ = registry.Close()
		return results, errors.New("全部已启用工具启动失败，请确认工具已安装并登录（详见各工具报错）")
	}

	s.toolsMu.Lock()
	s.toolsRunning = true
	s.toolsReg = registry
	s.toolsMu.Unlock()
	s.log.Info("local ai tools started", port.F("enabled", len(enabled)), port.F("ready", okCount))
	s.emitEvent("status", s.Status())
	return results, nil
}

// StopTools 关闭预热的 MCP 子进程并释放工具运行时。CLI 模式无资源需释放。
func (s *Service) StopTools() {
	s.toolsMu.Lock()
	if !s.toolsRunning {
		s.toolsMu.Unlock()
		return
	}
	s.toolsRunning = false
	registry := s.toolsReg
	s.toolsReg = nil
	s.toolsMu.Unlock()

	if registry != nil {
		if err := registry.Close(); err != nil {
			s.log.Warn("close tools mcp registry failed", port.F("err", err.Error()))
		}
	}
	s.log.Info("local ai tools stopped")
	s.emitEvent("status", s.Status())
}

// secretsEnv 从配置密钥派发给 AI CLI 子进程的环境变量。
func (s *Service) secretsEnv(cfg *config.AgentConfig) map[string]string {
	env := map[string]string{}
	if cfg.Secrets.AnthropicAPIKey != "" {
		env["ANTHROPIC_API_KEY"] = cfg.Secrets.AnthropicAPIKey
	}
	if cfg.Secrets.OpenAIAPIKey != "" {
		env["OPENAI_API_KEY"] = cfg.Secrets.OpenAIAPIKey
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// BotTestResult 机器人连接测试结果（不含任何 secret）。
type BotTestResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Tenant  string `json:"tenant_key,omitempty"`
	Expire  int    `json:"expire_seconds,omitempty"`
}

// TestBot 不启动长连接，仅验证指定渠道当前配置的凭证是否可用。
// channel 留空时默认测飞书（兼容旧界面/旧调用）。
func (s *Service) TestBot(channel string) (BotTestResult, error) {
	if strings.TrimSpace(channel) == "" {
		channel = imbot.ChannelFeishu
	}
	if !imbot.IsSupported(channel) {
		return BotTestResult{Channel: channel, Detail: "未知渠道: " + channel},
			fmt.Errorf("未知 IM 渠道: %s", channel)
	}
	settings := channelBotConfig(s.getConfig(), channel)
	if strings.TrimSpace(settings.credID) == "" || strings.TrimSpace(settings.credSecret) == "" {
		return BotTestResult{Channel: channel, Detail: "该渠道凭证未配置"},
			errors.New(channel + " credentials missing")
	}
	creds := imbot.Credentials{}
	switch channel {
	case imbot.ChannelFeishu:
		creds = imbot.Credentials{AppID: settings.credID, AppSecret: settings.credSecret}
	case imbot.ChannelWeCom:
		creds = imbot.Credentials{BotID: settings.credID, BotSecret: settings.credSecret}
	}
	runner, err := imbot.New(imbot.Spec{
		Channel: channel, Creds: creds,
		OnMessage: func(context.Context, port.IMBotMessage) error { return nil },
		Log:       s.log,
	})
	if err != nil {
		return BotTestResult{Channel: channel, Detail: err.Error()}, err
	}
	tester, ok := runner.(port.IMBotCredentialTester)
	if !ok {
		return BotTestResult{Channel: channel, Detail: "该渠道不支持凭证测试"},
			errors.New("channel has no credential tester")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := tester.TestCredentials(ctx)
	if err != nil {
		return BotTestResult{Channel: channel, OK: false, Detail: r.Detail}, err
	}
	out := BotTestResult{
		Channel: channel, OK: true, Detail: r.Detail, Expire: r.ExpireSeconds,
	}
	if r.Extra != nil {
		out.Tenant = r.Extra["tenant_key"]
	}
	return out, nil
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
