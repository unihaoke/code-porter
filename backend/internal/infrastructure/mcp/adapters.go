package mcp

import (
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
)

// NewTraeAdapter 创建 Trae 适配器。
//
// mode=mcp：需在本机安装、登录并开启 MCP Server；command 指向其 MCP 启动入口。
// mode=cli：直接命令行调用 Trae CLI（预置模板为通用形态，需按实际版本调整 args）。
func NewTraeAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "trae-mcp")
	return NewAdapter(model.Trae, cfg, log)
}

// NewClaudeCodeAdapter 创建 Claude Code 适配器。
func NewClaudeCodeAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "claude")
	return NewAdapter(model.ClaudeCode, cfg, log)
}

// NewCodeBuddyAdapter 创建 CodeBuddy 适配器。
func NewCodeBuddyAdapter(cfg AdapterConfig, log port.Logger) *Adapter {
	cfg = applyDefault(cfg, "codebuddy-mcp")
	return NewAdapter(model.CodeBuddy, cfg, log)
}

// NewCodexAdapter 创建 Codex 适配器。
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

// ---------------------------------------------------------------------------
// CLI 模式预设
//
// CLI 模式不要求工具开启 MCP Server：只要本机装有 CLI 并已登录（有订阅/
// 登录态）即可调用，一次任务一个进程、用完退出。
//
// 重要：AI 密钥通常【不需要】填写。claude / codex 这类 CLI 默认读取本机登录态
// （OAuth / 订阅凭证）。只有在使用 --bare 模式或显式指定第三方 API Key 时才需要。
// ---------------------------------------------------------------------------

// claudeCLIArgs Claude Code 的 CLI 参数。
//
//	-p                         非交互一次性执行，打印结果后退出
//	--output-format stream-json 逐事件输出（assistant/user/result），支持真流式
//	--verbose                  输出完整 content blocks（text/thinking/tool_use/tool_result），
//	                           不加它拿不到思考过程与工具调用，只剩正文
//	--permission-mode          无人值守必须放开权限，否则 CLI 会卡在交互确认直到超时
//
// 解析器（extractCLIEvents）据此把思考/工具过程与正文分流：飞书卡片能实时展示
// 「思考中/调用工具」折叠过程，网关结果与 SSE 仍然只收正文。
func claudeCLIArgs() []string {
	return []string{"-p", "{{prompt}}", "--output-format", "stream-json", "--verbose"}
}

// legacyClaudeCLIArgs 流式改造前的旧默认参数（json 单次结果模式），用于自动迁移。
func legacyClaudeCLIArgs() []string {
	return []string{"-p", "{{prompt}}", "--output-format", "json"}
}

// equalArgs 比较两个参数模板是否完全一致。
func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// codexCLIArgs Codex 的 CLI 参数。
//
// codex 的非交互子命令是 exec；--json 输出结构化结果，--skip-git-repo-check
// 允许在非 git 目录执行。
func codexCLIArgs() []string {
	return []string{"exec", "{{prompt}}", "--json", "--skip-git-repo-check"}
}

// traeCLIArgs Trae 的通用 CLI 形态。
//
// Trae 未提供稳定的公开 CLI 文档，这里给出最常见的「子命令 + 提示词」形态，
// 实际 args 需按本机安装的版本调整（可用 `trae --help` 确认）。
func traeCLIArgs() []string {
	return []string{"-p", "{{prompt}}"}
}

// codebuddyCLIArgs CodeBuddy 的通用 CLI 形态（与 claude 风格一致）。
func codebuddyCLIArgs() []string {
	return []string{"-p", "{{prompt}}", "--output-format", "json"}
}

// NewCLIRunner 按模型与配置构造 CLI 模式适配器。
func NewCLIRunner(m model.Model, cfg AdapterConfig, log port.Logger) *CLIAdapter {
	cfg = applyCLIDefaults(m, cfg)
	return NewCLIAdapter(m, cfg, log)
}

// applyCLIDefaults 填充 CLI 模式下的默认命令与参数模板（用户已显式配置的不覆盖）。
func applyCLIDefaults(m model.Model, cfg AdapterConfig) AdapterConfig {
	if cfg.Mode == "" {
		cfg.Mode = ModeCLI
	}
	var defArgs []string
	switch m {
	case model.ClaudeCode:
		if cfg.Command == "" {
			cfg.Command = "claude"
		}
		// 旧版本（json 单次结果模式）落盘的默认参数自动升级到 stream-json --verbose；
		// 只精确匹配旧默认，用户自定义过的参数原样保留。
		if equalArgs(cfg.CLI.Args, legacyClaudeCLIArgs()) {
			cfg.CLI.Args = nil
			if cfg.CLI.OutputFormat == "json" {
				cfg.CLI.OutputFormat = ""
			}
		}
		defArgs = claudeCLIArgs()
		// Claude Code 未登录时用登录态即可，无需密钥；给个宽松的默认模型别名。
		if cfg.CLI.Model == "" {
			cfg.CLI.Model = "sonnet"
		}
		// 与默认参数保持一致：claude 走真流式，思考/工具过程才能被解析出来。
		if cfg.CLI.OutputFormat == "" {
			cfg.CLI.OutputFormat = "stream-json"
		}
	case model.Codex:
		if cfg.Command == "" {
			cfg.Command = "codex"
		}
		defArgs = codexCLIArgs()
	case model.Trae:
		if cfg.Command == "" {
			cfg.Command = "trae"
		}
		defArgs = traeCLIArgs()
	case model.CodeBuddy:
		if cfg.Command == "" {
			cfg.Command = "codebuddy"
		}
		defArgs = codebuddyCLIArgs()
	}
	if len(cfg.CLI.Args) == 0 {
		cfg.CLI.Args = defArgs
	}
	// Agent 无人看守：默认放开权限，避免 CLI 卡在交互式确认。
	// 如需保守行为，可在 yaml 里显式设为 acceptEdits 或 dontAsk。
	if cfg.CLI.PermissionMode == "" {
		cfg.CLI.PermissionMode = "bypassPermissions"
	}
	// 同步套用 CLI 子默认值（output_format / result_path），使返回的配置即完整可用。
	cfg.CLI = cfg.CLI.withDefaults()
	return cfg.withDefaults()
}
