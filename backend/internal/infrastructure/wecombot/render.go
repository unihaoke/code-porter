// 企业微信流式消息的 Markdown 渲染（纯函数，不接触 WebSocket）。
//
// 与飞书卡片不同，企微流式回复只有一条 Markdown 文本，过程区/正文/状态行
// 都在这一条 Markdown 里分区：
//   - 过程区：灰色（comment）引用块。思考阶段展示完整过程（截头保尾）；
//   - 一旦正文开始，过程折叠为最后几行的灰色摘要，置于正文之前；
//   - 失败：正文用 warning 红色字体提示；
//   - 运行中：底部灰色斜体状态行。
//
// 平台限制单帧 content ≤ 20480 字节（UTF-8），超长时先丢弃过程区，
// 再对正文截头保尾（最新输出永远可见）。
package wecombot

import (
	"strings"

	"github.com/codeporter/code-porter/internal/application/port"
)

const (
	// maxStreamContentBytes 平台 stream.content 硬上限。
	maxStreamContentBytes = 20480
	// streamContentSafetyMargin 给 JSON 转义/协议信封预留的余量（引号、换行转义后会膨胀）。
	streamContentSafetyMargin = 1024
	// processFullMaxRunes 思考阶段过程区最大字符数（截头保尾）。
	processFullMaxRunes = 1500
	// processSummaryMaxLines / processSummaryMaxRunes 正文出现后摘要的行数与字符上限。
	processSummaryMaxLines = 6
	processSummaryMaxRunes = 300
)

// 企微 markdown 支持的行内字体颜色：info 绿、comment 灰、warning 橙红。
const (
	colorGrey    = "comment"
	colorWarning = "warning"
)

// renderStream 按平台无关的 IMCardState 渲染一帧企微 Markdown。
// 输出保证不超过 maxStreamContentBytes（含安全余量）。
func renderStream(state port.IMCardState) string {
	running := state.Phase == port.IMCardRunning
	body := strings.TrimSpace(state.Body)
	hasBody := body != ""

	proc := ""
	if p := strings.TrimSpace(state.Process); p != "" {
		if running && !hasBody {
			proc = processBlock("💭 思考与执行过程", tailRunes(p, processFullMaxRunes))
		} else {
			proc = processBlock("💭 过程摘要", summarizeProcess(p))
		}
	}

	bodyBlock := ""
	switch {
	case hasBody && state.Phase == port.IMCardFailed:
		bodyBlock = font(colorWarning, body)
	case hasBody:
		bodyBlock = body
	case state.Phase == port.IMCardFailed:
		bodyBlock = font(colorWarning, "⚠️ 执行失败")
	case running:
		bodyBlock = font(colorGrey, "⏳ 已收到任务，正在等待本地 AI 输出…")
	}

	footer := ""
	if running {
		if f := strings.TrimSpace(state.Footer); f != "" {
			footer = font(colorGrey, f)
		} else {
			footer = font(colorGrey, "_正在处理…_")
		}
	}

	return capContent(proc, bodyBlock, footer, maxStreamContentBytes-streamContentSafetyMargin)
}

// processBlock 把过程文本渲染为灰色引用块；多行逐行加引用前缀。
func processBlock(title, content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return font(colorGrey, "**"+title+"**\n"+strings.Join(lines, "\n"))
}

// summarizeProcess 正文出现后的过程摘要：取最后若干行，再按字符数截头保尾。
func summarizeProcess(process string) string {
	lines := strings.Split(strings.TrimSpace(process), "\n")
	if len(lines) > processSummaryMaxLines {
		lines = lines[len(lines)-processSummaryMaxLines:]
	}
	return tailRunes(strings.Join(lines, "\n"), processSummaryMaxRunes)
}

// font 用企微 markdown 行内字体着色。
func font(color, s string) string {
	return `<font color="` + color + `">` + s + `</font>`
}

// capContent 组装三段内容并保证总字节数 ≤ limit：
// 先丢过程区，仍超长则对正文截头保尾（footer 很短，始终保留）。
func capContent(proc, body, footer string, limit int) string {
	if s := joinSections(proc, body, footer); len(s) <= limit {
		return s
	}
	// 1) 过程区是辅助信息，优先整体丢弃。
	if s := joinSections("", body, footer); len(s) <= limit {
		return s
	}
	// 2) 正文截头保尾：预留 footer、分隔换行与省略提示的空间。
	reserved := len(footer) + 16
	marker := "…（前面的内容已省略）\n"
	budget := limit - reserved
	if budget < len(marker)+8 {
		budget = len(marker) + 8 // 极端情况下也至少留一句提示
	}
	body = tailUTF8Bytes(body, budget-len(marker))
	if body != "" {
		body = marker + body
	}
	return joinSections("", body, footer)
}

// joinSections 用空行拼接非空段。
func joinSections(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// tailRunes 按 rune 截头保尾。
func tailRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…（前面的过程已省略）\n" + string(r[len(r)-max:])
}

// tailUTF8Bytes 保留字符串尾部不超过 max 字节的内容，并在 rune 边界截断。
func tailUTF8Bytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := []byte(s[len(s)-max:])
	// 切片起点可能落在多字节字符中间：跳过开头的 continuation 字节
	// （10xxxxxx），之后的首字节一定是某个完整字符的起点（尾部到串尾字节完整）。
	for len(b) > 0 && b[0]&0xC0 == 0x80 {
		b = b[1:]
	}
	return string(b)
}
