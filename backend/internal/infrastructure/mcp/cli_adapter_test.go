package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// fakeCLIEnv 标记「本进程被当作假 CLI 运行」。
const fakeCLIEnv = "CODEPORTER_FAKE_CLI"

// TestMain 让测试二进制在带标记时充当假 CLI，输出 claude 风格的 JSON 结果。
func TestMain(m *testing.M) {
	if os.Getenv(fakeCLIEnv) != "" {
		runFakeCLI()
		return
	}
	os.Exit(m.Run())
}

// runFakeCLI 打印收到的参数与一个 claude 风格的最终结果。
func runFakeCLI() {
	fmt.Fprintf(os.Stderr, "ARGV:%s\n", strings.Join(os.Args[1:], "\x1f"))
	prompt := os.Getenv("FAKE_PROMPT")
	// 模拟 stream-json 的一条 assistant 增量 + 一条最终 result。
	fmt.Println(`{"type":"assistant","message":{"content":[{"type":"text","text":"部分输出:"}]}}`)
	out := map[string]any{
		"type":     "result",
		"subtype":  "success",
		"result":   "最终答案(" + prompt + ")",
		"is_error": false,
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	os.Exit(0)
}

// newTestCLIAdapter 构造一个指向测试二进制自身的 CLI 适配器。
func newTestCLIAdapter(t *testing.T, cli CLIConfig) *CLIAdapter {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := AdapterConfig{
		Enabled:        true,
		Command:        self,
		WorkDir:        "", // 用进程 cwd
		RequestTimeout: 30 * time.Second,
		Env:            map[string]string{fakeCLIEnv: "1", "FAKE_PROMPT": "P"},
		CLI:            cli,
	}
	a := NewCLIAdapter(model.ClaudeCode, cfg, port.NopLogger{})
	a.lookPath = func(string) (string, error) { return self, nil }
	return a
}

func TestCLIBuildArgs(t *testing.T) {
	a := newTestCLIAdapter(t, CLIConfig{
		Args:           []string{"-p", "{{prompt}}", "--output-format", "json"},
		Model:          "sonnet",
		PermissionMode: "bypassPermissions",
		MaxTurns:       3,
		ExtraArgs:      []string{"--verbose"},
	})
	got := a.buildArgs("你好 世界")
	want := []string{"-p", "你好 世界", "--output-format", "json",
		"--model", "sonnet", "--permission-mode", "bypassPermissions",
		"--max-turns", "3", "--verbose"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("buildArgs =\n  %v\nwant\n  %v", got, want)
	}
}

func TestCLIBuildArgsPromptViaStdin(t *testing.T) {
	// 提示词走 stdin 时，argv 里的 {{prompt}} 占位符必须被移除（否则会留空串）。
	a := newTestCLIAdapter(t, CLIConfig{
		Args:           []string{"-p", "{{prompt}}", "--json"},
		PromptViaStdin: true,
	})
	got := a.buildArgs("忽略我")
	for _, g := range got {
		if g == "忽略我" {
			t.Errorf("提示词不应出现在 argv 中: %v", got)
		}
	}
}

func TestCLIRunEndToEnd(t *testing.T) {
	a := newTestCLIAdapter(t, CLIConfig{
		Args:          []string{"-p", "{{prompt}}", "--output-format", "json", "--model-flag"},
		IncludeStderr: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := a.StreamRun(ctx, port.MCPStreamRequest{
		TaskID:    "t1",
		Model:     model.ClaudeCode,
		Prompt:    "写个快排",
		Operation: task.OperationGenerate,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("unexpected error chunk: %v", c.Err)
		}
		sb.WriteString(c.Content)
	}
	got := sb.String()
	if !strings.Contains(got, "部分输出:") {
		t.Errorf("未收到流式增量, got=%q", got)
	}
	if !strings.Contains(got, "最终答案") {
		t.Errorf("未收到最终 result, got=%q", got)
	}
}

func TestCLIHealthCheck(t *testing.T) {
	a := newTestCLIAdapter(t, CLIConfig{Args: []string{"x"}})
	if err := a.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck 应通过（可执行文件存在）: %v", err)
	}
}

func TestCLIHealthCheckMissingCommand(t *testing.T) {
	a := newTestCLIAdapter(t, CLIConfig{})
	a.lookPath = exec.LookPath // 用真实查找，验证"未安装"分支
	a.cfg.Command = "definitely-not-a-real-binary-xyz"
	if err := a.HealthCheck(context.Background()); err == nil {
		t.Error("命令不存在时 HealthCheck 应报错")
	}
}

func TestCLIDisabled(t *testing.T) {
	a := newTestCLIAdapter(t, CLIConfig{Args: []string{"x"}})
	a.cfg.Enabled = false
	if _, err := a.StreamRun(context.Background(), port.MCPStreamRequest{}); err == nil {
		t.Error("禁用时应返回错误")
	}
}

func TestExtractCLIText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"claude result", `{"type":"result","result":"答案A"}`, "答案A"},
		{"claude delta", `{"type":"assistant","message":{"content":[{"type":"text","text":"增量B"}]}}`, "增量B"},
		{"generic text", `{"text":"文本C"}`, "文本C"},
		{"nested path", `{"data":{"answer":"嵌套D"}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ev map[string]any
			if err := json.Unmarshal([]byte(c.raw), &ev); err != nil {
				t.Fatal(err)
			}
			var got []string
			if c.name == "nested path" {
				got = extractCLIText(ev, "data.answer")
			} else {
				got = extractCLIText(ev, "result")
			}
			joined := strings.Join(got, "")
			if c.want != "" && !strings.Contains(joined, c.want) {
				t.Errorf("extractCLIText = %q, want contains %q", joined, c.want)
			}
		})
	}
}

func TestApplyCLIDefaultsClaude(t *testing.T) {
	cfg := applyCLIDefaults(model.ClaudeCode, AdapterConfig{Enabled: true})
	if cfg.Command != "claude" {
		t.Errorf("Command = %q, want claude", cfg.Command)
	}
	if cfg.CLI.OutputFormat != "json" {
		t.Errorf("OutputFormat = %q, want json", cfg.CLI.OutputFormat)
	}
	if cfg.CLI.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q, want bypassPermissions（无人值守必须放开权限）", cfg.CLI.PermissionMode)
	}
	joined := strings.Join(cfg.CLI.Args, " ")
	if !strings.Contains(joined, "{{prompt}}") {
		t.Errorf("Args 应含提示词占位符, got %v", cfg.CLI.Args)
	}
}

func TestRegistryDispatchesByMode(t *testing.T) {
	// cli 模式 -> CLIAdapter；未指定（缺省）-> MCP Adapter。
	cliReg := NewRegistry(Config{
		ClaudeCode: AdapterConfig{Enabled: true, Mode: ModeCLI},
	}, port.NopLogger{})
	got, err := cliReg.Get(model.ClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*CLIAdapter); !ok {
		t.Errorf("mode=cli 应产出 *CLIAdapter, got %T", got)
	}

	mcpReg := NewRegistry(Config{
		ClaudeCode: AdapterConfig{Enabled: true},
	}, port.NopLogger{})
	got2, err := mcpReg.Get(model.ClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got2.(*Adapter); !ok {
		t.Errorf("缺省应产出 *Adapter(MCP), got %T", got2)
	}
}

var _ port.MCPRunner = (*CLIAdapter)(nil)
