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
	"regexp"
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
	// 模型/工具输出里可能夹带模型自己写的 <font> 标签（等号两边带空格、
	// 不支持的颜色、未闭合等），企微对该标签是严格语法，任何变体都会被
	// 当纯文本原样显示。着色是本渲染器的职责，先对三段输入统一消毒。
	body := strings.TrimSpace(sanitizeFontTags(state.Body))
	hasBody := body != ""

	proc := ""
	if p := strings.TrimSpace(sanitizeFontTags(state.Process)); p != "" {
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
		if f := strings.TrimSpace(sanitizeFontTags(state.Footer)); f != "" {
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

var (
	// fontTagRe 匹配完整的 <font ...> / </font> 标签（容忍大小写与空白变体）。
	fontTagRe = regexp.MustCompile(`(?i)<\s*/?\s*font\b[^>]*>`)
	// fontColorRe 从开标签里提取 color 值（容忍 color = 'x'、color=x 等变体）。
	fontColorRe = regexp.MustCompile(`(?i)\bcolor\s*=\s*["']?([a-z]+)["']?`)
	// fontPartialRe 匹配帧尾被截断的半拉标签（流式切片所致），本帧先隐藏，
	// 下一帧标签完整后会自动正常渲染。
	fontPartialRe = regexp.MustCompile(`(?i)<\s*/?\s*font\b[^>]*$`)
)

// allowedFontColors 企微 markdown 仅支持的三种行内颜色。
var allowedFontColors = map[string]string{
	"info":    "info",
	"comment": "comment",
	"warning": "warning",
}

// sanitizeFontTags 消毒模型/工具输出中自带的 <font> 标签。
//
// 企微只认严格写法 <font color="info|comment|warning">（等号两侧无空格、
// 值带双引号），模型输出的 <font color = 'comment'>、<font color="red">
// 等变体不会被解析，标签会被当纯文本原样展示。处理规则：
//   - 合法颜色的变体写法规范化为严格语法；
//   - 非法颜色/残缺属性的 font 标签整体剥除（着色由渲染器负责）；
//   - 用栈配对开闭标签，丢弃非法开标签对应的闭标签，补齐未闭合的合法标签，
//     避免一个坏标签污染其后整段消息的渲染；
//   - 帧尾被流式截断的半拉标签本帧先移除。
func sanitizeFontTags(s string) string {
	if s == "" || !strings.Contains(strings.ToLower(s), "font") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	// stack 记录每个尚未闭合的开标签是否合法（true=输出了规范化开标签）。
	validStack := make([]bool, 0, 4)
	last := 0
	for _, m := range fontTagRe.FindAllStringIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		tag := strings.ToLower(s[m[0]:m[1]])
		last = m[1]
		if strings.HasPrefix(strings.TrimLeft(tag[1:], " \t"), "/") {
			// 闭标签：仅当其配对的开标签合法时才输出。
			if len(validStack) > 0 {
				valid := validStack[len(validStack)-1]
				validStack = validStack[:len(validStack)-1]
				if valid {
					b.WriteString("</font>")
				}
			}
			continue
		}
		color := ""
		if cm := fontColorRe.FindStringSubmatch(tag); cm != nil {
			color = allowedFontColors[cm[1]]
		}
		if color != "" {
			validStack = append(validStack, true)
			b.WriteString(`<font color="` + color + `">`)
		} else {
			validStack = append(validStack, false)
		}
	}
	b.WriteString(s[last:])
	out := b.String()

	// 补齐流式中途模型未闭合的合法开标签（逆序不影响结果：闭标签无属性）。
	for _, valid := range validStack {
		if valid {
			out += "</font>"
		}
	}
	// 去掉帧尾的半拉标签（如 '<font color="comm'）。
	out = fontPartialRe.ReplaceAllString(out, "")
	return out
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
