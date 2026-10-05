package wecombot

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/codeporter/code-porter/internal/application/port"
)

// TestRenderThinking 思考阶段：灰色引用区展示完整过程，正文占位，底部状态行。
func TestRenderThinking(t *testing.T) {
	out := renderStream(port.IMCardState{
		Phase: port.IMCardRunning, Process: "先分析需求\n再读文件",
		Footer: "🧠 _正在思考…_",
	})
	if !strings.Contains(out, `color="comment"`) || !strings.Contains(out, "> 先分析需求") {
		t.Fatalf("process must render as grey quote: %s", out)
	}
	if !strings.Contains(out, "等待本地 AI 输出") {
		t.Fatalf("empty body must show waiting placeholder: %s", out)
	}
	if !strings.Contains(out, "正在思考") {
		t.Fatalf("footer missing: %s", out)
	}
}

// TestRenderBodyCollapsesProcess 正文出现：过程折叠为摘要置于正文前，输出态 footer。
func TestRenderBodyCollapsesProcess(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "工具行")
	}
	out := renderStream(port.IMCardState{
		Phase:   port.IMCardRunning,
		Process: strings.Join(lines, "\n"),
		Body:    "答案是 42",
		Footer:  "✍️ _正在输出…_",
	})
	if !strings.Contains(out, "过程摘要") {
		t.Fatalf("process must collapse to summary once body starts: %s", out)
	}
	if strings.Contains(out, "思考与执行过程") {
		t.Fatalf("full-process title must not appear with body present: %s", out)
	}
	if !strings.Contains(out, "答案是 42") {
		t.Fatalf("body missing: %s", out)
	}
	// 摘要只保留最后 6 行。
	if c := strings.Count(out, "> 工具行"); c > processSummaryMaxLines {
		t.Fatalf("summary must keep at most %d lines, got %d", processSummaryMaxLines, c)
	}
	// 摘要必须排在正文之前。
	if strings.Index(out, "过程摘要") > strings.Index(out, "答案是 42") {
		t.Fatal("summary must appear before body")
	}
}

// TestRenderDone 终态：无 footer，正文保留，摘要仍在。
func TestRenderDone(t *testing.T) {
	out := renderStream(port.IMCardState{
		Phase: port.IMCardDone, Process: "做过 X", Body: "完成",
	})
	if strings.Contains(out, "_正在") {
		t.Fatalf("done message must not carry running footer: %s", out)
	}
	if !strings.Contains(out, "完成") || !strings.Contains(out, "过程摘要") {
		t.Fatalf("done message layout wrong: %s", out)
	}
}

// TestRenderFailed 失败：红色提示。
func TestRenderFailed(t *testing.T) {
	out := renderStream(port.IMCardState{
		Phase: port.IMCardFailed, Body: "❌ boom",
	})
	if !strings.Contains(out, `color="warning"`) || !strings.Contains(out, "boom") {
		t.Fatalf("failure must be rendered in warning color: %s", out)
	}

	// 无正文的失败也要有兜底红字。
	out2 := renderStream(port.IMCardState{Phase: port.IMCardFailed})
	if !strings.Contains(out2, `color="warning"`) {
		t.Fatalf("empty failure must still warn: %s", out2)
	}
}

// TestSummarizeProcess 摘要取最后 N 行并截头保尾。
func TestSummarizeProcess(t *testing.T) {
	long := strings.Repeat("很", processSummaryMaxRunes+50)
	got := summarizeProcess("l1\nl2\n" + long)
	if r := []rune(got); len(r) > processSummaryMaxRunes+20 { // 允许省略提示
		t.Fatalf("summary too long: %d runes", len(r))
	}
	if !strings.HasSuffix(strings.TrimSpace(got), strings.Repeat("很", 10)) {
		t.Fatal("summary must keep the tail")
	}
}

// TestCapContentDropsProcessThenBody 超长时先砍过程，再对正文截头保尾且 UTF-8 安全。
func TestCapContentDropsProcessThenBody(t *testing.T) {
	// 场景 1：过程 + 正文超长 → 过程被整体丢弃。
	proc := strings.Repeat("P", 1000)
	body := strings.Repeat("B", 1000)
	out := capContent(proc, body, "footer", 1500)
	if strings.Contains(out, "PPPP") {
		t.Fatal("process must be dropped first under the byte budget")
	}
	if !strings.Contains(out, strings.Repeat("B", 100)) {
		t.Fatal("body tail must be preserved")
	}

	// 场景 2：纯正文仍超长 → 截头保尾。
	big := strings.Repeat("中文", 5000) // 30000 字节，远超 20480
	out2 := capContent("", big, "", maxStreamContentBytes-streamContentSafetyMargin)
	if len(out2) > maxStreamContentBytes {
		t.Fatalf("output exceeds platform frame limit: %d bytes", len(out2))
	}
	if !utf8.ValidString(out2) {
		t.Fatal("truncation must remain valid UTF-8")
	}
	if !strings.Contains(out2, "已省略") {
		t.Fatal("truncation marker missing")
	}
	// 尾部必须是原始内容末尾。
	if !strings.HasSuffix(strings.TrimSpace(out2), "中文中文中文") {
		t.Fatal("tail must be preserved after truncation")
	}
}

// TestRenderStreamRespectsFrameLimit 端到端：超大状态渲染结果仍在平台限额内且合法。
func TestRenderStreamRespectsFrameLimit(t *testing.T) {
	out := renderStream(port.IMCardState{
		Phase:   port.IMCardRunning,
		Process: strings.Repeat("思考内容\n", 3000),
		Body:    strings.Repeat("输出内容", 4000),
		Footer:  "_正在输出…_",
	})
	if len(out) > maxStreamContentBytes {
		t.Fatalf("rendered stream content %d bytes exceeds limit %d", len(out), maxStreamContentBytes)
	}
	if !utf8.ValidString(out) {
		t.Fatal("rendered content must be valid UTF-8")
	}
}
