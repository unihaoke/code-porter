package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// newModelCLIAdapter 构造指定模型、指向测试二进制的 CLI 适配器（不真正执行）。
func newModelCLIAdapter(t *testing.T, m model.Model, cli CLIConfig) *CLIAdapter {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := NewCLIAdapter(m, AdapterConfig{
		Enabled: true, Command: self, RequestTimeout: 30 * time.Second, CLI: cli,
	}, port.NopLogger{})
	a.lookPath = func(string) (string, error) { return self, nil }
	return a
}

func joined(args []string) string { return strings.Join(args, "\x1f") }

func TestCLIPermissionClaude(t *testing.T) {
	// 模拟 applyCLIDefaults 后的 claude 配置（默认 bypassPermissions）。
	newA := func() *CLIAdapter {
		return newModelCLIAdapter(t, model.ClaudeCode, CLIConfig{
			Args:           []string{"-p", "{{prompt}}", "--output-format", "json"},
			PermissionMode: "bypassPermissions",
		})
	}

	t.Run("read forces plan even if yaml allows bypass", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionRead))
		if !strings.Contains(got, "--permission-mode\x1fplan") {
			t.Fatalf("read must force plan mode, got %v", strings.Split(got, "\x1f"))
		}
		if strings.Contains(got, "bypassPermissions") {
			t.Fatalf("read must not carry bypassPermissions: %v", got)
		}
	})

	t.Run("write forces acceptEdits", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionWrite))
		if !strings.Contains(got, "--permission-mode\x1facceptEdits") {
			t.Fatalf("write must force acceptEdits, got %v", strings.Split(got, "\x1f"))
		}
	})

	t.Run("all keeps yaml permission mode", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionAll))
		if !strings.Contains(got, "--permission-mode\x1fbypassPermissions") {
			t.Fatalf("all must keep configured mode, got %v", strings.Split(got, "\x1f"))
		}
	})
}

func TestCLIPermissionCodex(t *testing.T) {
	newA := func() *CLIAdapter {
		return newModelCLIAdapter(t, model.Codex, CLIConfig{
			Args:           []string{"exec", "{{prompt}}", "--json", "--skip-git-repo-check"},
			PermissionMode: "bypassPermissions", // 即便误配，codex 也不能收到该 flag
		})
	}

	t.Run("read enforces read-only sandbox", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionRead))
		if !strings.Contains(got, "--sandbox\x1fread-only") {
			t.Fatalf("codex read must use read-only sandbox, got %v", strings.Split(got, "\x1f"))
		}
		if !strings.Contains(got, "--ask-for-approval\x1fnever") {
			t.Fatalf("codex read must never block on approval prompts, got %v", strings.Split(got, "\x1f"))
		}
		if strings.Contains(got, "--permission-mode") {
			t.Fatalf("codex does not support --permission-mode: %v", got)
		}
	})

	t.Run("write enforces workspace-write sandbox", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionWrite))
		if !strings.Contains(got, "--sandbox\x1fworkspace-write") {
			t.Fatalf("codex write must use workspace-write sandbox, got %v", strings.Split(got, "\x1f"))
		}
	})

	t.Run("all leaves codex defaults untouched", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionAll))
		if strings.Contains(got, "--sandbox") {
			t.Fatalf("all must not inject sandbox flags: %v", got)
		}
	})
}

func TestCLIPermissionCodeBuddy(t *testing.T) {
	a := newModelCLIAdapter(t, model.CodeBuddy, CLIConfig{
		Args:           []string{"-p", "{{prompt}}", "--output-format", "json"},
		PermissionMode: "bypassPermissions",
	})
	if got := joined(a.buildArgs("p", task.PermissionRead)); !strings.Contains(got, "--permission-mode\x1fplan") {
		t.Fatalf("codebuddy read must map to plan mode, got %v", strings.Split(got, "\x1f"))
	}
}

func TestBuildPromptPermissionGuard(t *testing.T) {
	withPerm := func(p task.Permission) port.MCPStreamRequest {
		return port.MCPStreamRequest{Prompt: "帮我重构这个函数", Permission: p}
	}

	readPrompt := buildPrompt(withPerm(task.PermissionRead))
	if !strings.Contains(readPrompt, "只读") || !strings.Contains(readPrompt, "严禁") {
		t.Fatalf("read guard missing in prompt: %s", readPrompt)
	}
	if !strings.HasPrefix(readPrompt, "【运行权限：只读】") {
		t.Fatalf("guard must be prepended before user prompt, got prefix %q", readPrompt[:20])
	}

	writePrompt := buildPrompt(withPerm(task.PermissionWrite))
	if !strings.Contains(writePrompt, "工作目录") {
		t.Fatalf("write guard missing: %s", writePrompt)
	}

	allPrompt := buildPrompt(withPerm(task.PermissionAll))
	if strings.Contains(allPrompt, "运行权限") {
		t.Fatalf("all must not include guard text: %s", allPrompt)
	}
}
