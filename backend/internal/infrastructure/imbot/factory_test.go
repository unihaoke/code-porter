package imbot

import (
	"context"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
)

func nopHandler(context.Context, port.IMBotMessage) error { return nil }

func TestChannels(t *testing.T) {
	got := Channels()
	if len(got) != 2 || got[0] != ChannelFeishu || got[1] != ChannelWeCom {
		t.Fatalf("channels = %v", got)
	}
	if !IsSupported("feishu") || !IsSupported("wecom") || IsSupported("dingtalk") {
		t.Fatal("IsSupported mismatch")
	}
}

func TestCredentialID(t *testing.T) {
	c := Credentials{AppID: "cli_x", BotID: "bot_y"}
	if c.CredentialID(ChannelFeishu) != "cli_x" {
		t.Fatal("feishu identity must be app id")
	}
	if c.CredentialID(ChannelWeCom) != "bot_y" {
		t.Fatal("wecom identity must be bot id")
	}
	if c.CredentialID("other") != "" {
		t.Fatal("unknown channel identity must be empty")
	}
}

func TestNewRejectsInvalidSpec(t *testing.T) {
	if _, err := New(Spec{Channel: ChannelFeishu}); err == nil {
		t.Fatal("nil handler must fail")
	}
	if _, err := New(Spec{Channel: ChannelFeishu, OnMessage: nopHandler}); err == nil {
		t.Fatal("nil logger must fail")
	}
	if _, err := New(Spec{
		Channel: "dingtalk", OnMessage: nopHandler, Log: port.NopLogger{},
	}); err == nil {
		t.Fatal("unknown channel must fail")
	}
}

func TestNewConstructsRunners(t *testing.T) {
	r1, err := New(Spec{
		Channel:   ChannelFeishu,
		Creds:     Credentials{AppID: "cli_a", AppSecret: "sec"},
		OnMessage: nopHandler, Log: port.NopLogger{},
	})
	if err != nil {
		t.Fatalf("feishu runner: %v", err)
	}
	if _, ok := r1.(port.IMBotCredentialTester); !ok {
		t.Fatal("feishu runner must support credential testing")
	}

	r2, err := New(Spec{
		Channel:   ChannelWeCom,
		Creds:     Credentials{BotID: "bot_a", BotSecret: "sec"},
		OnMessage: nopHandler, Log: port.NopLogger{},
	})
	if err != nil {
		t.Fatalf("wecom runner: %v", err)
	}
	if _, ok := r2.(port.IMBotCredentialTester); !ok {
		t.Fatal("wecom runner must support credential testing")
	}
	pacer, ok := r2.(port.IMBotCardPacer)
	if !ok {
		t.Fatal("wecom runner must expose card pacer")
	}
	if pacer.CardFlushInterval() <= 0 || pacer.MaxCardLifetime() <= 0 {
		t.Fatal("wecom pacer timings must be positive")
	}
}

// TestNewMissingChannelCredentials 凭证缺失由各渠道 runner 校验并报错。
func TestNewMissingChannelCredentials(t *testing.T) {
	if _, err := New(Spec{
		Channel: ChannelFeishu, OnMessage: nopHandler, Log: port.NopLogger{},
	}); err == nil {
		t.Fatal("feishu requires app id/secret")
	}
	if _, err := New(Spec{
		Channel: ChannelWeCom, OnMessage: nopHandler, Log: port.NopLogger{},
	}); err == nil {
		t.Fatal("wecom requires bot id/secret")
	}
}
