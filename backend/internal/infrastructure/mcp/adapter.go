package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// AdapterConfig 单个 MCP 适配器的配置。
type AdapterConfig struct {
	// Enabled 是否启用该适配器。
	Enabled bool `yaml:"enabled"`
	// Mode 调用方式：mcp（默认，走 MCP stdio 协议）或 cli（直接命令行调用）。
	// CLI 模式不要求工具开启 MCP Server，只要有可执行 CLI 且已登录即可。
	Mode InvokeMode `yaml:"mode,omitempty"`
	// Command 本地 MCP Server 启动命令（如 trae / claude / npx），或 CLI 可执行名。
	Command string `yaml:"command"`
	// Args 命令参数。
	Args []string `yaml:"args"`
	// Env 附加环境变量。
	Env map[string]string `yaml:"env,omitempty"`
	// WorkDir 工作目录。
	WorkDir string `yaml:"work_dir"`
	// ToolName 指定调用的工具名；为空时自动从 tools/list 中挑选（仅 MCP 模式）。
	ToolName string `yaml:"tool_name"`
	// PromptArgument 工具入参中承载提示词的字段名（仅 MCP 模式）。
	PromptArgument string `yaml:"prompt_argument"`
	// ExtraArguments 附加到工具入参的固定字段（仅 MCP 模式）。
	ExtraArguments map[string]any `yaml:"extra_arguments"`
	// RequestTimeout 单次调用超时。
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// StartupTimeout 子进程启动与握手超时（仅 MCP 模式）。
	StartupTimeout time.Duration `yaml:"startup_timeout"`
	// CLI 直接命令行调用方式的配置（Mode=cli 时生效）。
	CLI CLIConfig `yaml:"cli,omitempty"`
}

func (c AdapterConfig) withDefaults() AdapterConfig {
	if c.PromptArgument == "" {
		c.PromptArgument = "prompt"
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 3 * time.Minute
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = 30 * time.Second
	}
	return c
}

// Adapter 通用 MCP 适配器：不同本地 AI 工具通过不同配置实例化。
type Adapter struct {
	model  model.Model
	cfg    AdapterConfig
	log    port.Logger
	mu     sync.Mutex
	client *StdioClient
	tool   string
}

// NewAdapter 构造适配器。
func NewAdapter(m model.Model, cfg AdapterConfig, log port.Logger) *Adapter {
	return &Adapter{
		model: m,
		cfg:   cfg.withDefaults(),
		log:   log.With(port.F("mcp", m.String())),
	}
}

// Model 返回适配器对应的本地 AI 工具。
func (a *Adapter) Model() model.Model { return a.model }

// StreamRun 执行一次流式调用。
func (a *Adapter) StreamRun(ctx context.Context, req port.MCPStreamRequest) (<-chan port.MCPChunk, error) {
	if !a.cfg.Enabled {
		return nil, apperr.New(apperr.CodeMCPFailure,
			"adapter for "+a.model.String()+" is disabled, please enable it in config")
	}
	client, err := a.ensureClient(ctx)
	if err != nil {
		return nil, err
	}
	tool, err := a.ensureTool(ctx, client)
	if err != nil {
		return nil, err
	}

	out := make(chan port.MCPChunk, 32)
	// 订阅服务端增量通知（日志/进度），实现真正的流式透传。
	unsubscribe := client.OnNotify(func(method string, params json.RawMessage) {
		text := extractNotifyText(method, params)
		if text == "" {
			return
		}
		select {
		case out <- port.MCPChunk{Seq: 0, Content: text}:
		case <-ctx.Done():
		}
	})

	arguments := a.buildArguments(req)
	go func() {
		defer func() {
			unsubscribe()
			close(out)
		}()
		result, cerr := client.Call(ctx, "tools/call", map[string]any{
			"name":      tool,
			"arguments": arguments,
		})
		if cerr != nil {
			select {
			case out <- port.MCPChunk{Err: apperr.Wrap(apperr.CodeMCPFailure,
				"mcp tool call failed: "+a.model.String(), cerr)}:
			case <-ctx.Done():
			}
			return
		}
		text := extractResultText(result)
		if text == "" {
			return
		}
		select {
		case out <- port.MCPChunk{Content: text}:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

// HealthCheck 探测本地 AI 软件 / MCP Server 是否可用。
func (a *Adapter) HealthCheck(ctx context.Context) error {
	if !a.cfg.Enabled {
		return apperr.New(apperr.CodeMCPFailure, a.model.String()+" adapter is disabled")
	}
	if a.cfg.Command == "" {
		return apperr.New(apperr.CodeMCPFailure, a.model.String()+" command is not configured")
	}
	if _, err := exec.LookPath(a.cfg.Command); err != nil {
		return apperr.Wrap(apperr.CodeMCPFailure,
			a.model.String()+" is not installed or not in PATH", err)
	}
	// 已启动过则直接探活，避免每次健康检查都拉起子进程。
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return nil
	}
	if _, err := client.Call(ctx, "tools/list", map[string]any{}); err != nil {
		return apperr.Wrap(apperr.CodeMCPFailure, a.model.String()+" mcp server is not responding", err)
	}
	return nil
}

// Close 释放子进程。
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	err := a.client.Close()
	a.client = nil
	a.tool = ""
	return err
}

func (a *Adapter) ensureClient(ctx context.Context) (*StdioClient, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	client := NewStdioClient(StdioConfig{
		Command:        a.cfg.Command,
		Args:           a.cfg.Args,
		Env:            a.cfg.Env,
		WorkDir:        a.cfg.WorkDir,
		StartupTimeout: a.cfg.StartupTimeout,
	})
	startCtx, cancel := context.WithTimeout(ctx, a.cfg.StartupTimeout)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		return nil, apperr.Wrap(apperr.CodeMCPFailure,
			"cannot start "+a.model.String()+" mcp server, is the app installed and running?", err)
	}
	a.client = client
	a.log.Info("mcp server started", port.F("command", a.cfg.Command))
	return client, nil
}

func (a *Adapter) ensureTool(ctx context.Context, client *StdioClient) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tool != "" {
		return a.tool, nil
	}
	if a.cfg.ToolName != "" {
		a.tool = a.cfg.ToolName
		return a.tool, nil
	}
	raw, err := client.Call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return "", apperr.Wrap(apperr.CodeMCPFailure, "list mcp tools failed", err)
	}
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return "", apperr.Wrap(apperr.CodeInternal, "decode tools/list failed", err)
	}
	if len(listed.Tools) == 0 {
		return "", apperr.New(apperr.CodeMCPFailure, a.model.String()+" exposes no mcp tool")
	}
	// 优先选择名称包含关键词的编码类工具，否则取第一个。
	for _, t := range listed.Tools {
		n := strings.ToLower(t.Name)
		if strings.Contains(n, "code") || strings.Contains(n, "chat") ||
			strings.Contains(n, "ask") || strings.Contains(n, "agent") {
			a.tool = t.Name
			return a.tool, nil
		}
	}
	a.tool = listed.Tools[0].Name
	return a.tool, nil
}

func (a *Adapter) buildArguments(req port.MCPStreamRequest) map[string]any {
	args := map[string]any{}
	for k, v := range a.cfg.ExtraArguments {
		args[k] = v
	}
	args[a.cfg.PromptArgument] = buildPrompt(req)
	if req.WorkDir != "" {
		args["workDir"] = req.WorkDir
	}
	if len(req.Files) > 0 {
		files := make([]map[string]string, 0, len(req.Files))
		for _, f := range req.Files {
			files = append(files, map[string]string{"path": f.Path, "content": f.Content})
		}
		args["files"] = files
	}
	if req.MaxTokens > 0 {
		args["maxTokens"] = req.MaxTokens
	}
	return args
}

// buildPrompt 按操作类型拼装提示词，让本地 AI 工具产出更符合预期的结果。
func buildPrompt(req port.MCPStreamRequest) string {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		var sb strings.Builder
		for _, m := range req.Messages {
			sb.WriteString(m.Role)
			sb.WriteString(": ")
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
		prompt = strings.TrimSpace(sb.String())
	}
	switch req.Operation {
	case task.OperationGenerate:
		return "请生成以下代码需求的实现：\n" + prompt
	case task.OperationRefactor:
		return "请重构以下代码并说明改动点：\n" + prompt
	case task.OperationDebug:
		return "请分析以下代码的问题并给出修复方案：\n" + prompt
	case task.OperationExplain:
		return "请解释以下代码：\n" + prompt
	case task.OperationReview:
		return "请对以下代码做一次评审，指出风险与改进项：\n" + prompt
	default:
		return prompt
	}
}

// extractResultText 从 tools/call 结果中提取文本。
func extractResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return string(raw)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// extractNotifyText 从服务端通知中提取增量文本。
func extractNotifyText(method string, params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	if method != "notifications/message" && method != "notifications/progress" &&
		!strings.HasPrefix(method, "notifications/") {
		return ""
	}
	var p struct {
		Data    string `json:"data"`
		Message string `json:"message"`
		Text    string `json:"text"`
		Delta   string `json:"delta"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	switch {
	case p.Delta != "":
		return p.Delta
	case p.Text != "":
		return p.Text
	case p.Data != "":
		return p.Data
	case p.Message != "":
		return p.Message
	}
	return ""
}

// 保证实现满足端口约定。
var _ port.MCPRunner = (*Adapter)(nil)
