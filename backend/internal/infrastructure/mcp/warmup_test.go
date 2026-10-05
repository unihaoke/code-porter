package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
)

// fakeWarmRunner 可编排 Warmup/Running 行为的假适配器，避免拉起真实子进程。
type fakeWarmRunner struct {
	m         model.Model
	warmErr   error
	running   bool
	warmCalls int
}

func (f *fakeWarmRunner) Model() model.Model { return f.m }
func (f *fakeWarmRunner) StreamRun(context.Context, port.MCPStreamRequest) (<-chan port.MCPChunk, error) {
	return nil, nil
}
func (f *fakeWarmRunner) HealthCheck(context.Context) error { return nil }
func (f *fakeWarmRunner) Close() error                      { return nil }
func (f *fakeWarmRunner) Warmup(context.Context) error {
	f.warmCalls++
	return f.warmErr
}
func (f *fakeWarmRunner) Running() bool { return f.running }

// TestWarmupEnabledOnlyStartsEnabled 验证预热严格跳过未启用工具，
// 单个失败不影响其他工具，结果按启用集合返回。
func TestWarmupEnabledOnlyStartsEnabled(t *testing.T) {
	boom := errors.New("handshake failed")
	ok := &fakeWarmRunner{m: model.Trae}
	bad := &fakeWarmRunner{m: model.ClaudeCode, warmErr: boom}
	off := &fakeWarmRunner{m: model.Codex} // 配置中未启用

	cfg := Config{
		Trae:       AdapterConfig{Enabled: true, Mode: ModeMCP},
		ClaudeCode: AdapterConfig{Enabled: true, Mode: ModeCLI, StartupTimeout: 0},
		Codex:      AdapterConfig{Enabled: false},
	}.withDefaults()
	r := &Registry{runners: map[model.Model]port.MCPRunner{}, cfg: cfg}
	r.register(ok)
	r.register(bad)
	r.register(off)

	res := r.WarmupEnabled(context.Background())
	if len(res) != 2 {
		t.Fatalf("expected 2 results for enabled tools, got %d: %+v", len(res), res)
	}
	if off.warmCalls != 0 {
		t.Fatal("disabled tool must not be warmed up")
	}
	if ok.warmCalls != 1 || bad.warmCalls != 1 {
		t.Fatalf("enabled tools must be warmed exactly once: ok=%d bad=%d", ok.warmCalls, bad.warmCalls)
	}
	byModel := map[model.Model]WarmupResult{}
	for _, w := range res {
		byModel[w.Model] = w
	}
	if !byModel[model.Trae].OK {
		t.Fatal("trae warmup should succeed")
	}
	if byModel[model.ClaudeCode].OK || byModel[model.ClaudeCode].Detail == "" {
		t.Fatal("claude warmup should report failure detail")
	}
}

// TestWarmupEnabledAllDisabled 返回空结果，保证「没有启用工具」时不做任何预热。
func TestWarmupEnabledAllDisabled(t *testing.T) {
	r := &Registry{runners: map[model.Model]port.MCPRunner{}, cfg: Config{}.withDefaults()}
	r.register(&fakeWarmRunner{m: model.Trae})
	if got := r.WarmupEnabled(context.Background()); len(got) != 0 {
		t.Fatalf("expected no warmup results when all disabled, got %+v", got)
	}
}

// TestRunningModels 仅上报声明自己处于常驻态的适配器。
func TestRunningModels(t *testing.T) {
	up := &fakeWarmRunner{m: model.Trae, running: true}
	down := &fakeWarmRunner{m: model.Codex, running: false}
	r := &Registry{runners: map[model.Model]port.MCPRunner{}}
	r.register(up)
	r.register(down)

	got := r.RunningModels()
	if !got[model.Trae] {
		t.Fatal("trae should be reported running")
	}
	if got[model.Codex] {
		t.Fatal("codex should not be reported running")
	}
}
