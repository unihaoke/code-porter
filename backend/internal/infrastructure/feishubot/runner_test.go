package feishubot

import (
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func strp(s string) *string { return &s }

func TestConvertEventText(t *testing.T) {
	ev := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: strp("ou_1")}},
			Message: &larkim.EventMessage{
				MessageId:   strp("om_1"),
				ChatId:      strp("oc_1"),
				ChatType:    strp("group"),
				MessageType: strp("text"),
				Content:     strp(`{"text":"@_user_1 帮我看下这个函数"}`),
				Mentions: []*larkim.MentionEvent{
					{Key: strp("@_user_1"), Name: strp("CodePorter")},
				},
			},
		},
	}
	msg := convertEvent(ev)
	if msg == nil {
		t.Fatal("text event must convert")
	}
	if msg.EventID != "om_1" || msg.ChatID != "oc_1" {
		t.Fatalf("id mismatch: %+v", msg)
	}
	if msg.ChatType != "group" || !msg.Mentioned || msg.SenderID != "ou_1" {
		t.Fatalf("meta mismatch: %+v", msg)
	}
	if msg.Text != "帮我看下这个函数" {
		t.Fatalf("mention placeholder not stripped, got %q", msg.Text)
	}
	if msg.RawText == "" {
		t.Fatal("raw text must be preserved")
	}
}

func TestConvertEventP2P(t *testing.T) {
	ev := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId:   strp("om_2"),
				ChatId:      strp("oc_2"),
				ChatType:    strp("p2p"),
				MessageType: strp("text"),
				Content:     strp(`{"text":"你好"}`),
			},
		},
	}
	msg := convertEvent(ev)
	if msg == nil || msg.ChatType != "p2p" || msg.Mentioned {
		t.Fatalf("p2p conversion wrong: %+v", msg)
	}
}

func TestConvertEventSkipsNonTextAndMalformed(t *testing.T) {
	// 图片消息忽略。
	img := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Message: &larkim.EventMessage{MessageType: strp("image"), Content: strp(`{"key":"x"}`)},
	}}
	if convertEvent(img) != nil {
		t.Fatal("non-text must be ignored")
	}
	// 文本内容损坏忽略。
	bad := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Message: &larkim.EventMessage{MessageType: strp("text"), Content: strp(`{not json`)},
	}}
	if convertEvent(bad) != nil {
		t.Fatal("malformed content must be ignored")
	}
	// 空信封不 panic。
	if convertEvent(nil) != nil {
		t.Fatal("nil event must be ignored")
	}
}

func TestParseTextAndStrip(t *testing.T) {
	text, ok := parseTextContent(`{"text":"a @_user_1 b"}`)
	if !ok || text != "a @_user_1 b" {
		t.Fatalf("parse = %q,%v", text, ok)
	}
	if got := stripMentionPlaceholders(text, []string{"@_user_1"}); got != "a  b" {
		t.Fatalf("strip = %q", got)
	}
}

func TestBackoff(t *testing.T) {
	if backoff(0) != time.Second || backoff(1) != 2*time.Second {
		t.Fatal("backoff should double from 1s")
	}
	if backoff(100) != 60*time.Second {
		t.Fatal("backoff must cap at 60s")
	}
}

func TestNewRunnerValidation(t *testing.T) {
	if _, err := NewRunner("", "secret", nil, nil); err == nil {
		t.Fatal("empty app id must fail")
	}
	if _, err := NewRunner("cli_x", "", nil, nil); err == nil {
		t.Fatal("empty app secret must fail")
	}
	if _, err := NewRunner("cli_x", "secret", nil, nil); err == nil {
		t.Fatal("nil callback must fail")
	}
}
