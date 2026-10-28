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

	// --no-daemon 必须出现在 exec 子命令之前，防止 codex 共享 daemon 缓存沙箱策略。
	assertNoDaemonBeforeExec := func(t *testing.T, got string) {
		t.Helper()
		parts := strings.Split(got, "\x1f")
		noDaemonIdx, execIdx := -1, -1
		for i, p := range parts {
			if p == "--no-daemon" {
				noDaemonIdx = i
			}
			if p == "exec" {
				execIdx = i
			}
		}
		if noDaemonIdx == -1 {
			t.Fatalf("codex must carry --no-daemon to avoid shared daemon caching sandbox policy, got %v", parts)
		}
		if execIdx != -1 && noDaemonIdx > execIdx {
			t.Fatalf("--no-daemon must precede exec subcommand, got %v", parts)
		}
	}

	t.Run("read enforces read-only sandbox", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionRead))
		assertNoDaemonBeforeExec(t, got)
		if !strings.Contains(got, "--sandbox\x1fread-only") {
			t.Fatalf("codex read must use read-only sandbox, got %v", strings.Split(got, "\x1f"))
		}
		// codex 0.164 起没有 --ask-for-approval，审批策略只能走 -c approval_policy。
		// 值不带引号（与 orca 的 codex-read-only-app-server-args 一致）。
		if !strings.Contains(got, "-c\x1fapproval_policy=never") {
			t.Fatalf("codex read must never block on approval prompts, got %v", strings.Split(got, "\x1f"))
		}
		if !strings.Contains(got, "--dangerously-bypass-hook-trust") {
			t.Fatalf("codex read must bypass hook trust prompts, got %v", strings.Split(got, "\x1f"))
		}
		if strings.Contains(got, "--ask-for-approval") {
			t.Fatalf("codex 0.164+ rejects --ask-for-approval (unexpected argument): %v", got)
		}
		if strings.Contains(got, "--permission-mode") {
			t.Fatalf("codex does not support --permission-mode: %v", got)
		}
	})

	t.Run("write enforces workspace-write sandbox", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionWrite))
		assertNoDaemonBeforeExec(t, got)
		if !strings.Contains(got, "--sandbox\x1fworkspace-write") {
			t.Fatalf("codex write must use workspace-write sandbox, got %v", strings.Split(got, "\x1f"))
		}
		if !strings.Contains(got, "-c\x1fapproval_policy=never") {
			t.Fatalf("codex write must never block on approval prompts, got %v", strings.Split(got, "\x1f"))
		}
		if !strings.Contains(got, "--dangerously-bypass-hook-trust") {
			t.Fatalf("codex write must bypass hook trust prompts, got %v", strings.Split(got, "\x1f"))
		}
	})

	t.Run("all uses dangerously-bypass-approvals-and-sandbox", func(t *testing.T) {
		got := joined(newA().buildArgs("p", task.PermissionAll))
		assertNoDaemonBeforeExec(t, got)
		// all 对标 orca yolo 模式：用 --dangerously-bypass-approvals-and-sandbox
		// 一次性绕过审批与沙箱，不再传 --sandbox danger-full-access。
		if !strings.Contains(got, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("codex all must use --dangerously-bypass-approvals-and-sandbox, got %v", strings.Split(got, "\x1f"))
		}
		if !strings.Contains(got, "--dangerously-bypass-hook-trust") {
			t.Fatalf("codex all must bypass hook trust prompts, got %v", strings.Split(got, "\x1f"))
		}
		// 全权限绕过 flag 已覆盖审批与沙箱，不应再出现 --sandbox / approval_policy。
		if strings.Contains(got, "--sandbox") {
			t.Fatalf("codex all must not pass --sandbox (bypass flag covers it): %v", got)
		}
		if strings.Contains(got, "approval_policy") {
			t.Fatalf("codex all must not pass approval_policy (bypass flag covers it): %v", got)
		}
		// 关键回归点：误配的 permission_mode 绝不能透传给 codex，
		// 否则 codex 报 "unexpected argument" 直接退出，任务结果为空。
		if strings.Contains(got, "--permission-mode") {
			t.Fatalf("codex must never receive --permission-mode: %v", got)
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
