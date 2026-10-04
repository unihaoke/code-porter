package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/mcp"
)

// CLIConnStatus 连通性测试结论。
type CLIConnStatus string

const (
	// ConnOK 调用成功并拿到返回内容。
	ConnOK CLIConnStatus = "ok"
	// ConnSkipped 该工具未启用，跳过。
	ConnSkipped CLIConnStatus = "skipped"
	// ConnMissing CLI 未安装或不在 PATH。
	ConnMissing CLIConnStatus = "missing"
	// ConnQuota 调用链通、但账号额度/配额不足。
	ConnQuota CLIConnStatus = "quota"
	// ConnAuth 认证失败。
	ConnAuth CLIConnStatus = "auth"
	// ConnBadWorkDir 工作目录不可用。
	ConnBadWorkDir CLIConnStatus = "bad_work_dir"
	// ConnFailed 其他失败（超时、CLI 报错等）。
	ConnFailed CLIConnStatus = "failed"
)

// CLIConnResult 单个工具的连通性测试结果。
type CLIConnResult struct {
	Model    model.Model
	Status   CLIConnStatus
	Detail   string
	WorkDir  string
	Output   string
	Duration time.Duration
}

// OK 返回是否真正调用成功。
func (r CLIConnResult) OK() bool { return r.Status == ConnOK }

// Summary 返回一行中文摘要。
func (r CLIConnResult) Summary() string {
	prefix := map[CLIConnStatus]string{
		ConnOK:         "✅ 可用",
		ConnSkipped:    "⏭  未启用",
		ConnMissing:    "❌ 未安装",
		ConnQuota:      "⚠️  额度/配额不足",
		ConnAuth:       "⚠️  认证失败",
		ConnBadWorkDir: "❌ 工作目录不可用",
		ConnFailed:     "❌ 调用失败",
	}[r.Status]
	s := fmt.Sprintf("%-12s %s", r.Model.String(), prefix)
	if r.WorkDir != "" {
		s += "  工作目录=" + r.WorkDir
	}
	if r.Detail != "" {
		s += "  " + r.Detail
	}
	return s
}

// cliTestProbePrompt 连通性测试用的探针提示词：要求极短回复，便于人工确认。
const cliTestProbePrompt = "只回复两个字：可以"

// CLIConnTestOptions 连通性测试参数。
type CLIConnTestOptions struct {
	// Timeout 单个工具的实测超时。
	Timeout time.Duration
	// Probe 实测提示词；为空用 cliTestProbePrompt。
	Probe string
	// SkipProbe 只做安装检测，不发起真实调用（不消耗额度）。
	SkipProbe bool
}

// TestCLIConnections 依次测试各本地 AI 工具的连通性。
//
// 分两步：
//  1. 安装检测（HealthCheck）：只看可执行文件是否存在，不消耗任何额度。
//  2. 实测调用：发一个极短提示词，验证「参数送达 + 能取回结果」的完整链路。
//
// 返回值顺序与 model.All() 一致，便于展示。
func TestCLIConnections(
	ctx context.Context,
	cfg *config.AgentConfig,
	log *logging.Logger,
	opts CLIConnTestOptions,
) []CLIConnResult {
	if opts.Timeout <= 0 {
		opts.Timeout = 90 * time.Second
	}
	if opts.Probe == "" {
		opts.Probe = cliTestProbePrompt
	}
	if log == nil {
		log = logging.New(os.Stdout, logging.LevelInfo)
	}

	// 用一份独立副本构造注册表，避免污染运行中的配置。
	local := *cfg
	if local.MCP.WorkDir == "" {
		local.MCP.WorkDir = defaultClientWorkDir()
	}
	// 实测时给足超时。
	local.MCP.HealthTimeout = 10 * time.Second
	reg := mcp.NewRegistry(local.MCP, log)
	defer func() { _ = reg.Close() }()

	results := make([]CLIConnResult, 0, len(model.All()))
	for _, m := range model.All() {
		results = append(results, testOneCLI(ctx, reg, m, &local, log, opts))
	}
	return results
}

// testOneCLI 测试单个工具。
func testOneCLI(
	ctx context.Context,
	reg *mcp.Registry,
	m model.Model,
	cfg *config.AgentConfig,
	log *logging.Logger,
	opts CLIConnTestOptions,
) CLIConnResult {
	acfg := cfg.MCP.For(m)
	res := CLIConnResult{Model: m, WorkDir: strings.TrimSpace(acfg.WorkDir)}

	if !acfg.Enabled {
		res.Status = ConnSkipped
		res.Detail = "（在配置中未启用）"
		return res
	}
	// 工作目录必须先校验，否则 CLI 报错会掩盖真实原因。
	if res.WorkDir != "" {
		fi, err := os.Stat(res.WorkDir)
		switch {
		case err != nil:
			res.Status = ConnBadWorkDir
			res.Detail = "目录不存在或不可访问"
			return res
		case !fi.IsDir():
			res.Status = ConnBadWorkDir
			res.Detail = "不是文件夹"
			return res
		}
	}

	runner, err := reg.Get(m)
	if err != nil {
		res.Status = ConnMissing
		res.Detail = "未注册该适配器"
		return res
	}

	// 第 1 步：安装检测（不消耗额度）。
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = runner.HealthCheck(probeCtx)
	cancel()
	if err != nil {
		msg := apperrMessage(err)
		if strings.Contains(strings.ToLower(msg), "not in path") ||
			strings.Contains(strings.ToLower(msg), "not installed") {
			res.Status = ConnMissing
			res.Detail = "未安装或不在 PATH（" + acfg.Command + "）"
			return res
		}
		// 其他健康探测失败不直接判死，继续尝试实测。
		log.Debug("cli health check warning", port.F("model", m.String()), port.F("err", msg))
	}

	if opts.SkipProbe {
		res.Status = ConnOK
		res.Detail = "已安装（未发起实测调用）"
		return res
	}

	// 第 2 步：实测调用。
	start := time.Now()
	runCtx, cancel2 := context.WithTimeout(ctx, opts.Timeout)
	defer cancel2()
	ch, err := runner.StreamRun(runCtx, port.MCPStreamRequest{
		TaskID:    "conn-test",
		Model:     m,
		Prompt:    opts.Probe,
		Operation: task.OperationExplain,
		WorkDir:   res.WorkDir,
	})
	if err != nil {
		res.Duration = time.Since(start)
		res.Status = classifyError(err.Error(), "")
		res.Detail = trim(err.Error(), 160)
		return res
	}
	var sb strings.Builder
	for c := range ch {
		if c.Err != nil {
			// 记录错误但继续看是否已有内容返回——
			// 像 403 额度不足这类情况，CLI 会把错误文案放进 result。
			if res.Detail == "" {
				res.Detail = trim(apperrMessage(c.Err), 160)
			}
			continue
		}
		sb.WriteString(c.Content)
	}
	res.Duration = time.Since(start)
	res.Output = strings.TrimSpace(sb.String())
	res.Status = classifyError(res.Detail, res.Output)
	if res.Detail == "" && res.Status == ConnOK {
		res.Detail = "返回 " + fmt.Sprint(len(res.Output)) + " 字符"
	}
	return res
}

// classifyError 根据错误文本与已取回的内容判定结论。
// 很多 CLI（尤其 claude）在额度/认证失败时，会把错误作为正常输出塞进
// result 字段，只有退出码非 0。因此必须同时看"内容"才能给出准确结论。
func classifyError(detail, output string) CLIConnStatus {
	hay := strings.ToLower(detail + " " + output)
	switch {
	case strings.Contains(hay, "free quota exhausted"),
		strings.Contains(hay, "quota"),
		strings.Contains(hay, "insufficient credit"),
		strings.Contains(hay, "billing"),
		strings.Contains(hay, "exhausted"):
		// 额度问题说明认证已通过、链路是通的。
		return ConnQuota
	case strings.Contains(hay, "workdir"),
		strings.Contains(hay, "no such file or directory"),
		strings.Contains(hay, "cannot find the path"):
		return ConnBadWorkDir
	case strings.Contains(hay, "401"),
		strings.Contains(hay, "unauthorized"),
		strings.Contains(hay, "authentication"),
		strings.Contains(hay, "api key"),
		strings.Contains(hay, "not logged in"),
		strings.Contains(hay, "login"):
		return ConnAuth
	case strings.Contains(hay, "not found in path"),
		strings.Contains(hay, "executable file not found"),
		strings.Contains(hay, "is not recognized"):
		return ConnMissing
	}
	if strings.TrimSpace(output) != "" {
		return ConnOK
	}
	return ConnFailed
}

// defaultClientWorkDir 客户端 exe 所在目录，用作工作目录兜底。
func defaultClientWorkDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// apperrMessage 安全提取错误信息。
func apperrMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// trim 截断过长文本，避免撑爆弹窗。
func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatCLIConnReport 把测试结果格式化为可读文本（写入日志区）。
func FormatCLIConnReport(results []CLIConnResult) string {
	var sb strings.Builder
	sb.WriteString("==== 本地 AI 连通性测试 ====\r\n")
	for _, r := range results {
		sb.WriteString(r.Summary())
		sb.WriteString("\r\n")
		if out := strings.TrimSpace(r.Output); out != "" && r.Status != ConnOK {
			sb.WriteString("    返回: " + trim(out, 200) + "\r\n")
		}
	}
	return sb.String()
}

// summarizeCLIConn 统计结果，用于弹窗标题。
func summarizeCLIConn(results []CLIConnResult) (ok, missing, warned, failed int) {
	for _, r := range results {
		switch r.Status {
		case ConnOK:
			ok++
		case ConnSkipped:
			// 跳过不计入统计
		case ConnMissing, ConnBadWorkDir:
			missing++
		case ConnQuota, ConnAuth:
			warned++
		default:
			failed++
		}
	}
	return ok, missing, warned, failed
}
