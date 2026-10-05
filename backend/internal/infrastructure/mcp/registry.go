package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// Config MCP 适配层整体配置。
type Config struct {
	// Trae Trae 适配器配置。
	Trae AdapterConfig `yaml:"trae"`
	// ClaudeCode Claude Code 适配器配置。
	ClaudeCode AdapterConfig `yaml:"claude_code"`
	// CodeBuddy CodeBuddy 适配器配置。
	CodeBuddy AdapterConfig `yaml:"codebuddy"`
	// Codex Codex 适配器配置。
	Codex AdapterConfig `yaml:"codex"`
	// WorkDir 全局工作目录：本地 AI CLI 在此目录下执行。
	// 单个适配器的 work_dir 非空时以它为准。留空则回退为「客户端 exe 所在目录」，
	// 避免双击启动时继承到不确定的当前目录。
	WorkDir string `yaml:"work_dir"`
	// Env 注入到所有适配器的额外环境变量（如 AI 密钥 ANTHROPIC_API_KEY）。
	Env map[string]string `yaml:"env,omitempty"`
	// HealthTimeout 单次健康探测超时。
	HealthTimeout time.Duration `yaml:"health_timeout"`
}

// For 返回指定模型对应的适配器配置。
func (c Config) For(m model.Model) AdapterConfig {
	switch m {
	case model.ClaudeCode:
		return c.ClaudeCode
	case model.CodeBuddy:
		return c.CodeBuddy
	case model.Codex:
		return c.Codex
	case model.Trae:
		return c.Trae
	default:
		return AdapterConfig{}
	}
}

// resolvedWorkDir 返回适配器最终生效的工作目录。
// 优先级：适配器自身 work_dir > 全局 work_dir > 客户端 exe 所在目录 > 当前目录。
func (c Config) resolvedWorkDir(a AdapterConfig) string {
	if strings.TrimSpace(a.WorkDir) != "" {
		return a.WorkDir
	}
	if strings.TrimSpace(c.WorkDir) != "" {
		return c.WorkDir
	}
	return defaultWorkDir()
}

// defaultWorkDir 客户端 exe 所在目录；取不到时回退当前目录。
// 双击启动时进程 cwd 由 Explorer 决定（常是用户目录甚至 System32），
// 因此必须显式指定，否则 AI CLI 会在一个随意的目录里工作。
func defaultWorkDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// withDefaults 补全全局默认值。
func (c Config) withDefaults() Config {
	if c.HealthTimeout <= 0 {
		c.HealthTimeout = 5 * time.Second
	}
	return c
}

// withEnv 把全局 WorkDir / Env 合并进每个适配器（适配器自身设置优先）。
func (c Config) withEnv() Config {
	// 注意：不能因为 Env 为空就提前返回——全局 work_dir 仍需下发。
	merge := func(a AdapterConfig) AdapterConfig {
		// 全局 work_dir 作为兜底，不覆盖适配器自身的设置。
		if strings.TrimSpace(a.WorkDir) == "" && strings.TrimSpace(c.WorkDir) != "" {
			a.WorkDir = c.WorkDir
		}
		if len(c.Env) == 0 {
			return a
		}
		if a.Env == nil {
			a.Env = make(map[string]string, len(c.Env))
		}
		for k, v := range c.Env {
			if _, ok := a.Env[k]; !ok {
				a.Env[k] = v
			}
		}
		return a
	}
	c.Trae = merge(c.Trae)
	c.ClaudeCode = merge(c.ClaudeCode)
	c.CodeBuddy = merge(c.CodeBuddy)
	c.Codex = merge(c.Codex)
	return c
}

// Registry MCP 适配器注册表：按 model 路由到具体适配器。
type Registry struct {
	mu      sync.RWMutex
	runners map[model.Model]port.MCPRunner
	order   []model.Model
	cfg     Config
	log     port.Logger
}

// NewRegistry 按配置构建全部适配器。
//
// 每个工具按 mode 字段选择实现：
//   - mode=mcp（缺省）：常驻 MCP stdio 子进程，行为与原有一致
//   - mode=cli：一次性命令行调用，无需工具开启 MCP Server
func NewRegistry(cfg Config, log port.Logger) *Registry {
	cfg = cfg.withDefaults().withEnv()
	r := &Registry{
		runners: make(map[model.Model]port.MCPRunner, 4),
		cfg:     cfg,
		log:     log.With(port.F("cmp", "mcp_registry")),
	}
	r.register(newRunner(model.Trae, cfg.Trae, cfg, log))
	r.register(newRunner(model.ClaudeCode, cfg.ClaudeCode, cfg, log))
	r.register(newRunner(model.CodeBuddy, cfg.CodeBuddy, cfg, log))
	r.register(newRunner(model.Codex, cfg.Codex, cfg, log))
	return r
}

// newRunner 按 mode 构造对应模式的适配器。
func newRunner(m model.Model, aCfg AdapterConfig, c Config, log port.Logger) port.MCPRunner {
	// 先把工作目录解析成具体路径：留空时回退到 exe 目录，
	// 否则双击启动会继承 Explorer 给的随机 cwd。
	aCfg.WorkDir = c.resolvedWorkDir(aCfg)
	if InvokeMode(aCfg.Mode) == ModeCLI {
		return NewCLIRunner(m, aCfg, log)
	}
	switch m {
	case model.ClaudeCode:
		return NewClaudeCodeAdapter(aCfg, log)
	case model.CodeBuddy:
		return NewCodeBuddyAdapter(aCfg, log)
	case model.Codex:
		return NewCodexAdapter(aCfg, log)
	default:
		return NewTraeAdapter(aCfg, log)
	}
}

func (r *Registry) register(runner port.MCPRunner) {
	r.runners[runner.Model()] = runner
	r.order = append(r.order, runner.Model())
}

// Get 获取适配器。
func (r *Registry) Get(m model.Model) (port.MCPRunner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runner, ok := r.runners[m]
	if !ok {
		return nil, apperr.New(apperr.CodeNotFound, "no mcp adapter for model "+m.String())
	}
	return runner, nil
}

// All 返回全部适配器。
func (r *Registry) All() []port.MCPRunner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]port.MCPRunner, 0, len(r.order))
	for _, m := range r.order {
		out = append(out, r.runners[m])
	}
	return out
}

// HealthCheckAll 并发探测全部适配器可用性。
func (r *Registry) HealthCheckAll(ctx context.Context) []port.AdapterHealth {
	runners := r.All()
	timeout := r.cfg.HealthTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	results := make([]port.AdapterHealth, len(runners))
	var wg sync.WaitGroup
	for i, runner := range runners {
		wg.Add(1)
		go func(idx int, rn port.MCPRunner) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res := port.AdapterHealth{Model: rn.Model(), Available: true}
			if err := rn.HealthCheck(probeCtx); err != nil {
				res.Available = false
				res.Detail = apperr.MessageOf(err)
			}
			results[idx] = res
		}(i, runner)
	}
	wg.Wait()
	return results
}

// WarmupResult 单个适配器的预热结果。
type WarmupResult struct {
	Model  model.Model
	Mode   InvokeMode
	OK     bool
	Detail string
}

// warmer 适配器可选实现的预热能力（Adapter / CLIAdapter 均已实现）。
type warmer interface {
	Warmup(ctx context.Context) error
}

// WarmupEnabled 并发预热全部「已启用」的适配器：
// MCP 模式拉起常驻子进程并完成握手，CLI 模式做一次免额度的可执行性探测。
// 未启用的工具直接跳过（不出现在结果里）；单个失败不影响其他工具。
func (r *Registry) WarmupEnabled(ctx context.Context) []WarmupResult {
	type target struct {
		idx int
		rn  port.MCPRunner
		cfg AdapterConfig
	}
	targets := make([]target, 0, 4)
	for i, rn := range r.All() {
		acfg := r.cfg.For(rn.Model())
		if !acfg.Enabled {
			continue
		}
		targets = append(targets, target{idx: i, rn: rn, cfg: acfg})
	}
	results := make([]WarmupResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, rn port.MCPRunner, acfg AdapterConfig) {
			defer wg.Done()
			mode := acfg.Mode
			if mode == "" {
				mode = ModeMCP
			}
			// 子进程冷启动 + initialize + tools/list 可能较慢，
			// 在启动超时基础上留出余量；总时限仍受父 ctx 约束。
			timeout := acfg.StartupTimeout + 15*time.Second
			if timeout <= 15*time.Second {
				timeout = 45 * time.Second
			}
			warmCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res := WarmupResult{Model: rn.Model(), Mode: mode, OK: true}
			w, ok := rn.(warmer)
			if !ok {
				res.OK = false
				res.Detail = "该适配器不支持预热"
			} else if err := w.Warmup(warmCtx); err != nil {
				res.OK = false
				res.Detail = apperr.MessageOf(err)
			} else {
				if mode == ModeCLI {
					res.Detail = "CLI 可执行，已就绪"
				} else {
					res.Detail = "MCP Server 已启动并完成握手"
				}
			}
			results[idx] = res
		}(i, t.rn, t.cfg)
	}
	wg.Wait()
	return results
}

// runnerState 适配器可选实现的常驻态查询（Adapter 支持；CLIAdapter 恒为 false）。
type runnerState interface {
	Running() bool
}

// RunningModels 返回当前拥有常驻子进程（已拉起并握手）的模型集合。
func (r *Registry) RunningModels() map[model.Model]bool {
	out := make(map[model.Model]bool, len(r.order))
	for _, m := range r.order {
		if rs, ok := r.runners[m].(runnerState); ok && rs.Running() {
			out[m] = true
		}
	}
	return out
}

// Close 释放全部适配器资源。
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var lastErr error
	for _, m := range r.order {
		if err := r.runners[m].Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
