package wecombot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
)

func msgFrame(t *testing.T, body any, reqID string) frame {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return frame{Cmd: cmdMsgCallback, Headers: map[string]string{"req_id": reqID}, Body: raw}
}

func TestConvertMessageSingle(t *testing.T) {
	b := incomingMsgBody{
		MsgID: "m1", ChatID: "oc_should_not_use", ChatType: "single",
		MsgType: "text", Text: textContent{Content: "你好"},
	}
	b.From.UserID = "u123"
	f := msgFrame(t, b, "req-1")

	msg, err := convertMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("text message must convert")
	}
	if msg.ChatID != "u123" {
		t.Fatalf("single chat target must be sender userid, got %q", msg.ChatID)
	}
	if msg.ChatType != port.ChatP2P || msg.Mentioned {
		t.Fatalf("single chat meta wrong: %+v", msg)
	}
	if msg.EventID != "m1" || msg.Text != "你好" || msg.SenderID != "u123" {
		t.Fatalf("fields wrong: %+v", msg)
	}
	if msg.ReplyToken != "req-1" {
		t.Fatalf("req_id must pass through as reply token, got %q", msg.ReplyToken)
	}
}

func TestConvertMessageGroupStripsMention(t *testing.T) {
	b := incomingMsgBody{
		MsgID: "m2", ChatID: "oc_g", ChatType: "group",
		MsgType: "text", Text: textContent{Content: "@CodePorter 帮我看下这个函数"},
	}
	b.From.UserID = "u9"
	f := msgFrame(t, b, "req-2")

	msg, err := convertMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ChatID != "oc_g" {
		t.Fatalf("group chat target must be chatid, got %q", msg.ChatID)
	}
	if msg.ChatType != port.ChatGroup || !msg.Mentioned {
		t.Fatalf("group meta wrong: %+v", msg)
	}
	if msg.Text != "帮我看下这个函数" {
		t.Fatalf("mention prefix not stripped, got %q", msg.Text)
	}
	// 正文内部的 @人 不能被误删。
	f2 := msgFrame(t, incomingMsgBody{
		MsgID: "m3", ChatID: "oc_g", ChatType: "group",
		MsgType: "text", Text: textContent{Content: "@CodePorter 请 @张三 看下"},
	}, "req-3")
	msg2, err := convertMessage(f2)
	if err != nil {
		t.Fatal(err)
	}
	if msg2.Text != "请 @张三 看下" {
		t.Fatalf("inline mention must be preserved, got %q", msg2.Text)
	}
}

func TestConvertMessageSkipsNonText(t *testing.T) {
	f := msgFrame(t, incomingMsgBody{MsgID: "m4", ChatID: "oc_g", ChatType: "group", MsgType: "image"}, "r")
	msg, err := convertMessage(f)
	if err != nil || msg != nil {
		t.Fatalf("non-text must be ignored, got msg=%+v err=%v", msg, err)
	}
}

func TestConvertMessageErrors(t *testing.T) {
	// cmd 不符。
	if _, err := convertMessage(frame{Cmd: cmdEventCallback}); err == nil {
		t.Fatal("wrong cmd must error")
	}
	// body 损坏。
	if _, err := convertMessage(frame{Cmd: cmdMsgCallback, Body: []byte("{bad")}); err == nil {
		t.Fatal("malformed body must error")
	}
	// 单聊无 userid → 无回复目标。
	empty := msgFrame(t, incomingMsgBody{ChatType: "single", MsgType: "text", Text: textContent{Content: "x"}}, "r")
	if _, err := convertMessage(empty); err == nil {
		t.Fatal("single chat without userid must error")
	}
}

func TestSubscribeFrame(t *testing.T) {
	f := newSubscribeFrame("bot1", "sec1", "req-s")
	if f.Cmd != cmdSubscribe || f.reqID() != "req-s" {
		t.Fatalf("frame envelope wrong: %+v", f)
	}
	var b subscribeBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.BotID != "bot1" || b.Secret != "sec1" {
		t.Fatalf("subscribe body wrong: %+v", b)
	}
	// 信封必须能被平台解析。
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"aibot_subscribe"`) {
		t.Fatalf("serialized frame missing cmd: %s", raw)
	}
}

func TestStreamFrame(t *testing.T) {
	f := newStreamFrame("req-cb", "wec_abc", true, "**答案**")
	if f.Cmd != cmdRespondMsg || f.reqID() != "req-cb" {
		t.Fatalf("stream frame must carry callback req_id: %+v", f)
	}
	var b struct {
		MsgType string `json:"msgtype"`
		Stream  struct {
			ID      string `json:"id"`
			Finish  bool   `json:"finish"`
			Content string `json:"content"`
		} `json:"stream"`
	}
	if err := json.Unmarshal(f.Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.MsgType != "stream" || b.Stream.ID != "wec_abc" || !b.Stream.Finish || b.Stream.Content != "**答案**" {
		t.Fatalf("stream body wrong: %+v", b)
	}
}

func TestSendFrameWithoutReqID(t *testing.T) {
	f := newSendMarkdownFrame("oc_g", "hi")
	if f.Cmd != cmdSendMsg || f.reqID() != "" {
		t.Fatalf("proactive push must not carry req_id: %+v", f)
	}
	var b sendMsgBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.ChatID != "oc_g" || b.MsgType != "markdown" || b.Markdown.Content != "hi" {
		t.Fatalf("send body wrong: %+v", b)
	}
}

func TestEventType(t *testing.T) {
	raw, _ := json.Marshal(incomingEventBody{AIBotID: "b", EventType: eventDisconnected})
	f := frame{Cmd: cmdEventCallback, Body: raw}
	if got := eventType(f); got != eventDisconnected {
		t.Fatalf("event type = %q", got)
	}
	if got := eventType(frame{Cmd: cmdMsgCallback}); got != "" {
		t.Fatalf("non-event frame must yield empty type, got %q", got)
	}
}
