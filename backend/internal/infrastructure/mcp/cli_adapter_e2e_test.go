package mcp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// TestCLIAdapterRealClaude 用本机真实的 claude CLI 跑一次端到端调用。
//
// 默认跳过，避免在 CI / 无该工具的机器上失败：
// 设置环境变量 CODEPORTER_E2E_CLI=1 后执行。
//
//	go test ./internal/infrastructure/mcp/ -run TestCLIAdapterRealClaude -v
//
// 注意：本用例不校验模型回答内容，只校验「参数送达 + 有输出返回」，
// 因此即使账号额度用尽（CLI 返回 403 文本）也算通过——
// 那恰好说明调用链是通的，且证明【不需要配置 API 密钥】。
func TestCLIAdapterRealClaude(t *testing.T) {
	if os.Getenv("CODEPORTER_E2E_CLI") != "1" {
		t.Skip("设置 CODEPORTER_E2E_CLI=1 后执行真实 CLI 调用")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("本机未安装 claude CLI")
	}
	cfg := applyCLIDefaults(model.ClaudeCode, AdapterConfig{
		Enabled:        true,
		Mode:           ModeCLI,
		RequestTimeout: 3 * time.Minute,
	})
	cfg.CLI.IncludeStderr = true // 诊断用
	a := NewCLIAdapter(model.ClaudeCode, cfg, port.NopLogger{})

	if err := a.HealthCheck(context.Background()); err != nil {
		t.Fatalf("健康探测失败: %v", err)
	}
	t.Logf("实际 argv = %v", a.buildArgs("<PROMPT>", task.PermissionAll))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ch, err := a.StreamRun(ctx, port.MCPStreamRequest{
		TaskID:    "e2e",
		Model:     model.ClaudeCode,
		Prompt:    "只回复两个字：可以",
		Operation: task.OperationGenerate,
	})
	if err != nil {
		t.Fatalf("启动 CLI 失败: %v", err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.Err != nil {
			t.Logf("（收到错误片段，属正常：额度/网络问题）: %v", c.Err)
			continue
		}
		sb.WriteString(c.Content)
	}
	got := strings.TrimSpace(sb.String())
	if got == "" {
		t.Fatal("CLI 未返回任何内容")
	}
	t.Logf("CLI 返回 %d 字符: %s", len(got), truncate(got, 300))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
