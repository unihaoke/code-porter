package mcp

import (
	"context"
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
	// HealthTimeout 单次健康探测超时。
	HealthTimeout time.Duration `yaml:"health_timeout"`
}

func (c Config) withDefaults() Config {
	if c.HealthTimeout <= 0 {
		c.HealthTimeout = 5 * time.Second
	}
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
func NewRegistry(cfg Config, log port.Logger) *Registry {
	cfg = cfg.withDefaults()
	r := &Registry{
		runners: make(map[model.Model]port.MCPRunner, 4),
		cfg:     cfg,
		log:     log.With(port.F("cmp", "mcp_registry")),
	}
	r.register(NewTraeAdapter(cfg.Trae, log))
	r.register(NewClaudeCodeAdapter(cfg.ClaudeCode, log))
	r.register(NewCodeBuddyAdapter(cfg.CodeBuddy, log))
	r.register(NewCodexAdapter(cfg.Codex, log))
	return r
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
