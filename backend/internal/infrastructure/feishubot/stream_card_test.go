package feishubot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
)

// TestRenderStreamCardRunning 处理中：schema 2.0 + streaming_mode，思考面板展开、正文锚点存在。
func TestRenderStreamCardRunning(t *testing.T) {
	card := renderStreamCard(port.IMCardState{
		Phase:         port.IMCardRunning,
		Title:         "CodePorter",
		Summary:       "思考中",
		Process:       "我先分析一下",
		ProcessActive: true,
		Body:          "",
		Footer:        "🧠 _正在思考…_",
	})

	if card["schema"] != "2.0" {
		t.Fatalf("schema = %v, want 2.0", card["schema"])
	}
	cfg, _ := card["config"].(map[string]any)
	if cfg["streaming_mode"] != true || cfg["update_multi"] != true {
		t.Fatalf("running card must enable streaming mode: %v", cfg)
	}
	if cfg["width_mode"] != "fill" {
		t.Fatalf("width_mode = %v, want fill", cfg["width_mode"])
	}
	header, _ := card["header"].(map[string]any)
	if header["template"] != "blue" {
		t.Fatalf("running header template = %v, want blue", header["template"])
	}

	raw := toJSON(card)
	for _, want := range []string{
		"collapsible_panel", "cp_process", `"expanded":true`, "cp_body",
		"已收到任务，正在等待本地 AI 输出", "正在思考",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("running card missing %q\n%s", want, raw)
		}
	}
}

// TestRenderStreamCardDone 完成态：退出 streaming_mode、面板折叠、头部变绿、footer 消失。
func TestRenderStreamCardDone(t *testing.T) {
	card := renderStreamCard(port.IMCardState{
		Phase: port.IMCardDone, Summary: "已完成",
		Process: "过程", ProcessActive: false, Body: "最终答案",
	})
	cfg, _ := card["config"].(map[string]any)
	if cfg["streaming_mode"] != false {
		t.Fatal("done card must leave streaming mode")
	}
	header, _ := card["header"].(map[string]any)
	if header["template"] != "green" {
		t.Fatalf("done header = %v, want green", header["template"])
	}
	raw := toJSON(card)
	if !strings.Contains(raw, `"expanded":false`) {
		t.Fatal("process panel must collapse when done")
	}
	if strings.Contains(raw, "正在输出") {
		t.Fatal("footer must be absent when done")
	}
}

// TestRenderStreamCardFailed 失败态头部变红。
func TestRenderStreamCardFailed(t *testing.T) {
	card := renderStreamCard(port.IMCardState{Phase: port.IMCardFailed, Body: "❌ boom"})
	header, _ := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Fatalf("failed header = %v, want red", header["template"])
	}
}

// TestMaskSensitive 裸邮箱必须脱敏（飞书审计会因 EMAIL_ADDRESS 拒绝卡片）。
func TestMaskSensitive(t *testing.T) {
	in := "联系我 alice@example.com 或 bob@x.co 谢谢"
	out := maskSensitive(in)
	if strings.Contains(out, "alice@example.com") || strings.Contains(out, "@x.co") {
		t.Fatalf("email not masked: %q", out)
	}
	if !strings.Contains(out, "alice＠example.com") {
		t.Fatalf("masked form wrong: %q", out)
	}
	if maskSensitive("no email here") != "no email here" {
		t.Fatal("plain text should be unchanged")
	}
}

// TestTruncateRunes 过程区超长截头保尾。
func TestTruncateRunes(t *testing.T) {
	long := strings.Repeat("あ", streamProcessMaxRunes+10)
	got := truncateRunes(long, streamProcessMaxRunes)
	if !strings.Contains(got, "已省略") {
		t.Fatal("expected truncation marker")
	}
	if n := len([]rune(got)); n > streamProcessMaxRunes+20 {
		t.Fatalf("truncated too long: %d", n)
	}
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
