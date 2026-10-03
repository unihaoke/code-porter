package mcp

import (
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
)

// NewTraeAdapter 创建 Trae MCP 适配器。
//
// Trae 需在本机安装、登录并开启 MCP Server；command 指向其 MCP 启动入口。
func NewTraeAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "trae-mcp")
	return NewAdapter(model.Trae, cfg, log)
}

// NewClaudeCodeAdapter 创建 Claude Code MCP 适配器。
func NewClaudeCodeAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "claude")
	return NewAdapter(model.ClaudeCode, cfg, log)
}

// NewCodeBuddyAdapter 创建 CodeBuddy MCP 适配器。
func NewCodeBuddyAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "codebuddy-mcp")
	return NewAdapter(model.CodeBuddy, cfg, log)
}

// NewCodexAdapter 创建 Codex MCP 适配器。
func NewCodexAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "codex")
	return NewAdapter(model.Codex, cfg, log)
}

// applyDefault 在用户未显式配置时填入该工具的默认启动命令。
// 工具名保持为空，由 Adapter 通过 tools/list 自动探测，降低配置负担。
func applyDefault(cfg AdapterConfig, command string) AdapterConfig {
	if cfg.Command == "" {
		cfg.Command = command
	}
	return cfg.withDefaults()
}
