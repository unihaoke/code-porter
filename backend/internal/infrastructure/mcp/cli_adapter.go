package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// InvokeMode 适配器的调用方式。
type InvokeMode string

const (
	// ModeMCP 以 MCP stdio 协议调用（默认，兼容原有行为）。
	ModeMCP InvokeMode = "mcp"
	// ModeCLI 直接以命令行参数调用本地 AI 工具的 CLI（一次性非交互执行）。
	ModeCLI InvokeMode = "cli"
)

// CLIConfig 直接 CLI 调用方式的配置。
//
// 与 MCP 模式的关键差异：CLI 模式不需要工具开启 MCP Server，
// 只要本机装好 CLI 并已登录（有订阅/登录态）即可，调用结束进程即退出。
type CLIConfig struct {
	// Args 参数模板。占位符 {{prompt}} 会被替换为拼装好的提示词；
	// 若 PromptViaStdin 为 true，则 {{prompt}} 会被移除、提示词改走标准输入。
	Args []string `yaml:"args"`
	// PromptViaStdin 提示词是否通过标准输入传入（而非 argv）。
	// 对含空格/超长提示词更安全，也避免命令行长度上限。
	PromptViaStdin bool `yaml:"prompt_via_stdin"`
	// Model 模型别名/全名，映射到 --model。为空则不传。
	Model string `yaml:"model"`
	// PermissionMode 无人值守运行所需的权限模式，映射到 --permission-mode。
	// 取值如 bypassPermissions / acceptEdits / dontAsk。空则不传。
	// 注意：Agent 无人看守，若不放开权限，CLI 可能卡在交互式确认上直到超时。
	PermissionMode string `yaml:"permission_mode"`
	// MaxTurns 单次会话最大轮数，映射到 --max-turns。0 表示不限制。
	MaxTurns int `yaml:"max_turns"`
	// ExtraArgs 追加的固定参数（已展开的 argv 元素，不做占位替换）。
	ExtraArgs []string `yaml:"extra_args"`
	// OutputFormat 输出格式：text / json / stream-json。
	OutputFormat string `yaml:"output_format"`
	// ResultPath 从 JSON 结果中取正文的点分路径，如 "result"。
	// 为空时默认 "result"。
	ResultPath string `yaml:"result_path"`
	// IncludeStderr 是否把 stderr 也作为片段回传（调试用）。
	IncludeStderr bool `yaml:"include_stderr"`
}

func (c CLIConfig) withDefaults() CLIConfig {
	if c.OutputFormat == "" {
		c.OutputFormat = "json"
	}
	if c.ResultPath == "" {
		c.ResultPath = "result"
	}
	return c
}

// CLIAdapter 通过命令行直接调用本地 AI 工具的 CLI。
//
// 生命周期与 MCP 模式不同：每次 StreamRun 启动一个一次性进程、传入参数、
// 从 stdout 流式读取结果，进程退出即结束。因此不需要常驻子进程，
// Close 只做无操作。
type CLIAdapter struct {
	model model.Model
	cfg   AdapterConfig
	cli   CLIConfig
	log   port.Logger
	// lookPath 便于测试注入。
	lookPath func(string) (string, error)
}

// NewCLIAdapter 构造 CLI 直调适配器。
func NewCLIAdapter(m model.Model, cfg AdapterConfig, log port.Logger) *CLIAdapter {
	return &CLIAdapter{
		model:    m,
		cfg:      cfg.withDefaults(),
		cli:      cfg.CLI.withDefaults(),
		log:      log.With(port.F("adapter", "cli"), port.F("model", m.String())),
		lookPath: exec.LookPath,
	}
}

// Model 返回适配器对应的本地 AI 工具。
func (a *CLIAdapter) Model() model.Model { return a.model }

// Close 一次性进程无需常驻清理，空实现。
func (a *CLIAdapter) Close() error { return nil }

// HealthCheck 探测 CLI 是否已安装并可执行。
//
// 只做「可执行文件存在性 + 版本号可获取」两项检查，不发起真实推理请求，
// 因此不消耗额度。登录态是否有效留到首次真实调用时由 CLI 自身报错。
func (a *CLIAdapter) HealthCheck(ctx context.Context) error {
	if !a.cfg.Enabled {
		return apperr.New(apperr.CodeMCPFailure, a.model.String()+" cli adapter is disabled")
	}
	if a.cfg.Command == "" {
		return apperr.New(apperr.CodeMCPFailure, a.model.String()+" cli command is not configured")
	}
	path, err := a.lookPath(a.cfg.Command)
	if err != nil {
		return apperr.Wrap(apperr.CodeMCPFailure,
			a.model.String()+" cli is not installed or not in PATH", err)
	}
	// 试跑 --version：能输出即认为可正常执行。个别 CLI 无此参数，忽略失败。
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, path, "--version")
	cmd.SysProcAttr = stdioSysProcAttr()
	out, err := cmd.Output()
	if err != nil {
		// 不支持 --version 不代表不可用，仅记录不阻断。
		a.log.Debug("cli --version probe failed, assuming usable",
			port.F("command", a.cfg.Command), port.F("err", apperr.MessageOf(err)))
		return nil
	}
	ver := strings.TrimSpace(firstLine(string(out)))
	a.log.Info("cli detected", port.F("command", a.cfg.Command),
		port.F("version", ver), port.F("path", path))
	return nil
}

// promptPlaceholder 提示词在参数模板中的占位符。
const promptPlaceholder = "{{prompt}}"

// 任务权限 → CLI 权限模式/沙箱参数的硬映射常量。
//
// Claude Code（--permission-mode）：
//
//	plan             只读计划模式：不写文件、不执行命令（read）
//	acceptEdits      自动接受工作区文件编辑，其余操作仍被拒绝（write）
//	bypassPermissions 全部放开（all，沿用 yaml 配置，默认值）
//
// Codex（--sandbox + --ask-for-approval）：
//
//	read-only / never       完全只读，越界操作直接拒绝而非挂起（read）
//	workspace-write / never 仅工作区可写，越界拒绝（write）
//	all 时不传沙箱参数，保持 codex exec 自身默认行为不变。
const (
	claudePermissionRead  = "plan"
	claudePermissionWrite = "acceptEdits"

	codexSandboxRead   = "read-only"
	codexSandboxWrite  = "workspace-write"
	codexApprovalNever = "never"
)

// buildArgs 拼装最终 argv：展开占位符并追加派生参数。
//
// perm 是任务携带的权限上限，对支持的工具做硬强制（优先级高于 yaml 中的
// permission_mode 配置——配置只能给 all 放行，不能把 read 任务放宽）。
func (a *CLIAdapter) buildArgs(prompt string, perm task.Permission) []string {
	args := make([]string, 0, len(a.cli.Args)+8)
	for _, raw := range a.cli.Args {
		if strings.Contains(raw, promptPlaceholder) {
			// 提示词走 stdin 时移除该占位符，避免 argv 里出现空串。
			if a.cli.PromptViaStdin {
				continue
			}
			args = append(args, strings.ReplaceAll(raw, promptPlaceholder, prompt))
			continue
		}
		args = append(args, raw)
	}
	if a.cli.Model != "" {
		args = append(args, "--model", a.cli.Model)
	}

	permissionMode := a.cli.PermissionMode
	var hardSandbox []string
	switch perm {
	case task.PermissionRead, task.PermissionWrite:
		switch a.model {
		case model.ClaudeCode, model.CodeBuddy:
			if perm == task.PermissionRead {
				permissionMode = claudePermissionRead
			} else {
				permissionMode = claudePermissionWrite
			}
		case model.Codex:
			// codex 不认 --permission-mode，改用 sandbox 表达；沙箱参数追加在最后。
			permissionMode = ""
			sandbox := codexSandboxWrite
			if perm == task.PermissionRead {
				sandbox = codexSandboxRead
			}
			hardSandbox = []string{"--sandbox", sandbox, "--ask-for-approval", codexApprovalNever}
		}
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	if a.cli.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(a.cli.MaxTurns))
	}
	args = append(args, a.cli.ExtraArgs...)
	args = append(args, hardSandbox...)
	return args
}

// StreamRun 执行一次 CLI 调用，边跑边回传输出。
func (a *CLIAdapter) StreamRun(ctx context.Context, req port.MCPStreamRequest) (<-chan port.MCPChunk, error) {
	if !a.cfg.Enabled {
		return nil, apperr.New(apperr.CodeMCPFailure,
			"cli adapter for "+a.model.String()+" is disabled, please enable it in config")
	}
	if a.cfg.Command == "" {
		return nil, apperr.New(apperr.CodeMCPFailure,
			a.model.String()+" cli command is not configured")
	}
	prompt := buildPrompt(req)
	perm := req.Permission
	if perm == "" {
		perm = task.PermissionAll
	}

	// 工作目录必须存在，否则 cmd.Start() 报的错很难看懂。
	// 显式校验并给出可操作的提示。
	if dir := strings.TrimSpace(a.cfg.WorkDir); dir != "" {
		if fi, statErr := os.Stat(dir); statErr != nil {
			return nil, apperr.Wrap(apperr.CodeMCPFailure,
				"工作目录不存在或不可访问，请在客户端重新选择: "+dir, statErr)
		} else if !fi.IsDir() {
			return nil, apperr.New(apperr.CodeMCPFailure,
				"工作目录不是文件夹，请在客户端重新选择: "+dir)
		}
	}

	// 超时 context 的 cancel 必须由收尾 goroutine 触发：
	// 若在此处 defer cancel()，函数一 return 就会取消 context，
	// 刚启动的 CLI 进程会立刻被杀掉（表现为永远拿不到任何输出）。
	runCtx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout)

	args := a.buildArgs(prompt, perm)
	a.log.Info("cli invoke start",
		port.F("command", a.cfg.Command),
		port.F("args", len(args)),
		port.F("work_dir", a.cfg.WorkDir),
		port.F("permission", perm.String()),
		port.F("prompt_len", len(prompt)))

	cmd := exec.CommandContext(runCtx, a.cfg.Command, args...)
	cmd.SysProcAttr = stdioSysProcAttr()
	cmd.Dir = a.cfg.WorkDir
	cmd.Env = environWith(a.cfg.Env)

	var stdin io.WriteCloser
	if a.cli.PromptViaStdin {
		stdin, _ = cmd.StdinPipe()
	}
	// 非 stdin 模式务必置空：许多 CLI（如 claude）会等待 stdin 输入，
	// 不关闭就会白等数秒才继续。
	// 注意：以下每条早退路径都必须 cancel()，否则 runCtx 会一直挂着直到超时，
	// 造成 context 泄漏（go vet 会报 lostcancel）。
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, apperr.Wrap(apperr.CodeInternal, "open cli stdout", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, apperr.Wrap(apperr.CodeInternal, "open cli stderr", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, apperr.Wrap(apperr.CodeMCPFailure,
			"cannot start "+a.model.String()+" cli, is it installed and in PATH?", err)
	}

	out := make(chan port.MCPChunk, 64)
	var wg sync.WaitGroup

	// 提示词写入 stdin（部分 CLI 只认 stdin）。
	// 注意：Stdin 保持 nil 时，os/exec 会自动给子进程接上 os.DevNull，
	// 语义等价于 `< /dev/null`。这一步很关键——像 claude 这样的 CLI 会等待
	// stdin 输入，若不关闭就会白等数秒才继续。
	if stdin != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.WriteString(stdin, prompt)
			_ = stdin.Close()
		}()
	}

	// stderr：收集用于报错诊断。
	var errTail strings.Builder
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if a.cli.IncludeStderr {
				emit(ctx, out, port.MCPChunk{Content: "[stderr] " + line})
			}
			appendTail(&errTail, line, 20)
		}
	}()

	// stdout：按输出格式解析。
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.consume(ctx, out, stdout)
	}()

	go func() {
		defer cancel()
		wg.Wait()
		waitErr := cmd.Wait()
		// 注意：必须先把错误片段发完，再 close(out)。
		// 反序会触发 panic: send on closed channel。
		if waitErr != nil && runCtx.Err() == nil {
			emit(ctx, out, port.MCPChunk{Err: apperr.Wrap(apperr.CodeMCPFailure,
				a.model.String()+" cli exited abnormally",
				errors.New(strings.TrimSpace(errTail.String()+" "+waitErr.Error())))})
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			emit(ctx, out, port.MCPChunk{Err: apperr.New(apperr.CodeMCPFailure,
				a.model.String()+" cli timed out after "+a.cfg.RequestTimeout.String())})
		}
		close(out)
	}()
	return out, nil
}

// consume 读取 stdout 并按输出格式转换为片段。
func (a *CLIAdapter) consume(ctx context.Context, out chan<- port.MCPChunk, stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)
	// segCount 总产出片段数（含思考/工具），bodyCount 仅正文，用于终态去重与空输出兜底。
	segCount, bodyCount := 0, 0
	// tool_use_id → 工具名，跨事件累积，让 tool_result 能显示具体工具名。
	toolNames := make(map[string]string)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 非 JSON 输出（text 模式）直接按行透传，实现真流式。
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			if a.cli.OutputFormat == "text" {
				emit(ctx, out, port.MCPChunk{Content: line})
				segCount, bodyCount = segCount+1, bodyCount+1
			}
			continue
		}
		// 一行可能是单个 JSON 对象（--output-format json），
		// 也可能是事件数组（stream-json / 带 --verbose 时）——两种都要能解析。
		for _, ev := range parseJSONLine(trimmed) {
			for _, seg := range extractCLIEvents(ev, a.cli.ResultPath, toolNames) {
				if seg.text == "" {
					continue
				}
				// stream-json 下正文已随 assistant 事件流出，最终 result 是同一份全文，
				// 再发一遍会让结果重复；纯 json 模式（此前无正文）则以 result 为唯一正文。
				if seg.final && bodyCount > 0 {
					continue
				}
				emit(ctx, out, port.MCPChunk{Kind: seg.kind, Content: seg.text})
				segCount++
				if seg.kind.IsBody() {
					bodyCount++
				}
			}
		}
	}
	// 兜底：CLI 确实产出了内容但我们一条片段都没解析出来时，原样回吐最后一行，
	// 避免用户看到"明明有输出却一片空白"。
	if segCount == 0 {
		if raw := strings.TrimSpace(sc.Text()); raw != "" {
			emit(ctx, out, port.MCPChunk{Content: raw})
		}
	}
}

// parseJSONLine 解析一行 JSON，返回其中的事件对象列表。
//
// 兼容两种形状：
//   - 单个对象：{"type":"result","result":"..."}
//   - 事件数组：[{...},{...}]（claude 带 --verbose 或 stream-json 时的形态）
func parseJSONLine(line string) []map[string]any {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if strings.HasPrefix(line, "[") {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(line), &arr); err == nil {
			return arr
		}
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err == nil {
		return []map[string]any{obj}
	}
	return nil
}

// cliSeg 从一条 JSON 事件中解析出的展示片段。
type cliSeg struct {
	kind  port.ChunkKind
	text  string
	final bool // 来自最终 result 事件：若过程中已流出正文，则丢弃以免重复
}

// extractCLIEvents 从一条 JSON 事件中提取带类型的片段（正文 / 思考 / 工具过程）。
//
// 兼容 claude code `--output-format stream-json --verbose` 的事件：
//   - {"type":"assistant","message":{"content":[{"type":"text"|"thinking"|"tool_use",...}]}}
//   - {"type":"user","message":{"content":[{"type":"tool_result",...}]}}
//   - {"type":"result","result":"..."}（json 单对象模式的唯一正文，或 stream-json 的终态）
//
// toolNames 在多次调用间累积 tool_use_id → 工具名，供 tool_result 渲染可读行。
func extractCLIEvents(ev map[string]any, resultPath string, toolNames map[string]string) []cliSeg {
	var segs []cliSeg
	// 最终结果事件（claude/codex 的 {"type":"result","result":"..."}）。
	if t := lookupPath(ev, resultPath); t != "" {
		segs = append(segs, cliSeg{kind: port.ChunkText, text: t, final: ev["type"] == "result"})
		return segs
	}
	// 流式消息：assistant（text/thinking/tool_use）或 user（tool_result）。
	if msg, ok := ev["message"].(map[string]any); ok {
		if content, ok := msg["content"].([]any); ok {
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				switch cm["type"] {
				case "text":
					if s, ok := cm["text"].(string); ok && s != "" {
						segs = append(segs, cliSeg{kind: port.ChunkText, text: s})
					}
				case "thinking":
					if s, ok := cm["thinking"].(string); ok && s != "" {
						segs = append(segs, cliSeg{kind: port.ChunkThinking, text: s})
					}
				case "tool_use":
					id, _ := cm["id"].(string)
					name, _ := cm["name"].(string)
					if name != "" {
						if id != "" && toolNames != nil {
							toolNames[id] = name
						}
						segs = append(segs, cliSeg{kind: port.ChunkTool, text: toolUseLine(name, cm["input"])})
					}
				case "tool_result":
					id, _ := cm["tool_use_id"].(string)
					name := toolNames[id]
					if name == "" {
						name = "工具"
					}
					isErr, _ := cm["is_error"].(bool)
					mark, tail := "✅", "完成"
					if isErr {
						mark, tail = "❌", "失败"
					}
					segs = append(segs, cliSeg{kind: port.ChunkTool, text: mark + " " + name + " 执行" + tail})
				}
			}
		}
	}
	if len(segs) > 0 {
		return segs
	}
	// 通用增量字段（其他 CLI 的兼容兜底，统一按正文处理）。
	for _, k := range []string{"delta", "text", "content", "output_text"} {
		if m, ok := ev[k].(map[string]any); ok {
			if s, ok := m["text"].(string); ok && s != "" {
				segs = append(segs, cliSeg{kind: port.ChunkText, text: s})
			}
		}
		if s, ok := ev[k].(string); ok && s != "" && k != "content" {
			segs = append(segs, cliSeg{kind: port.ChunkText, text: s})
		}
	}
	return segs
}

// toolUseLine 生成工具调用的单行摘要：🔧 Read {"file_path":"..."}（参数截断）。
func toolUseLine(name string, input any) string {
	line := "🔧 " + name
	if input != nil {
		raw, err := json.Marshal(input)
		if err == nil && len(raw) > 2 {
			s := string(raw)
			if r := []rune(s); len(r) > 120 {
				s = string(r[:120]) + "…"
			}
			line += " " + s
		}
	}
	return line
}

// extractCLIText 兼容旧调用/测试：只返回正文类文本（思考与工具过程不计入）。
func extractCLIText(ev map[string]any, resultPath string) []string {
	var texts []string
	for _, seg := range extractCLIEvents(ev, resultPath, nil) {
		if seg.kind.IsBody() {
			texts = append(texts, seg.text)
		}
	}
	return texts
}

// extractJSONPath 按点分路径从 JSON 文本取字符串值。
func extractJSONPath(raw []byte, path string) string {
	if len(raw) == 0 || path == "" {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	return lookupPath(root, path)
}

// lookupPath 按点分路径取值。
func lookupPath(root map[string]any, path string) string {
	cur := any(root)
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[seg]
		if !ok {
			return ""
		}
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

// environWith 以当前环境为基底追加附加变量。
func environWith(extra map[string]string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// emit 尝试发送一个片段，ctx 结束则放弃。
func emit(ctx context.Context, out chan<- port.MCPChunk, c port.MCPChunk) {
	select {
	case out <- c:
	case <-ctx.Done():
	}
}

// appendTail 维护stderr 尾部若干行。
func appendTail(sb *strings.Builder, line string, maxLines int) {
	if line == "" {
		return
	}
	sb.WriteString(line)
	sb.WriteString("\n")
	all := strings.Split(strings.TrimRight(sb.String(), "\n"), "\n")
	if len(all) > maxLines {
		sb.Reset()
		sb.WriteString(strings.Join(all[len(all)-maxLines:], "\n"))
		sb.WriteString("\n")
	}
}

// firstLine 取第一行非空文本。
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

var _ port.MCPRunner = (*CLIAdapter)(nil)
