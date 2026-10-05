// Package imbot 是 IM 机器人渠道的注册中心（简单工厂 + 策略模式）：
// 应用层只依赖 port.IMBotRunner，新增渠道时在此 switch 注册，
// 编排层（cmd/agent）与配置/状态上报因此与具体平台解耦。
//
// 每个渠道包（feishubot/wecombot）各自实现连接、协议翻译与流式渲染，
// 工厂只负责按渠道名选择实现并把对应凭证字段映射进去。
package imbot

import (
	"fmt"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/infrastructure/feishubot"
	"github.com/codeporter/code-porter/internal/infrastructure/wecombot"
)

// 渠道名常量（同时作为配置键、IPC 参数、锁文件名的一部分）。
const (
	ChannelFeishu = "feishu"
	ChannelWeCom  = "wecom"
)

// Credentials 各渠道凭证的并集；工厂按渠道取用对应字段，未用字段留空。
type Credentials struct {
	// AppID/AppSecret 飞书自建应用凭证。
	AppID     string
	AppSecret string
	// BotID/BotSecret 企业微信智能机器人凭证（后台「API 模式 → 长连接」获取）。
	BotID     string
	BotSecret string
}

// CredentialID 返回该渠道用于展示与单实例锁的身份标识。
func (c Credentials) CredentialID(channel string) string {
	switch channel {
	case ChannelFeishu:
		return c.AppID
	case ChannelWeCom:
		return c.BotID
	default:
		return ""
	}
}

// Spec 构造一个渠道 runner 所需的全部输入。
type Spec struct {
	Channel   string
	Creds     Credentials
	OnMessage port.IMMessageHandler
	Log       port.Logger
}

// New 按渠道构造 runner（简单工厂）。返回的 runner 通常还实现可选能力
// port.IMBotCredentialTester / port.IMBotCardPacer，使用方按需断言。
func New(spec Spec) (port.IMBotRunner, error) {
	if spec.OnMessage == nil {
		return nil, fmt.Errorf("imbot: onMessage callback is required")
	}
	if spec.Log == nil {
		return nil, fmt.Errorf("imbot: logger is required")
	}
	switch spec.Channel {
	case ChannelFeishu:
		return feishubot.NewRunner(spec.Creds.AppID, spec.Creds.AppSecret, spec.OnMessage, spec.Log)
	case ChannelWeCom:
		return wecombot.NewRunner(spec.Creds.BotID, spec.Creds.BotSecret, spec.OnMessage, spec.Log)
	default:
		return nil, fmt.Errorf("imbot: unknown channel %q (supported: %v)", spec.Channel, Channels())
	}
}

// Channels 返回已注册的全部渠道（顺序稳定，用于状态枚举与配置默认值）。
func Channels() []string {
	return []string{ChannelFeishu, ChannelWeCom}
}

// IsSupported 判断渠道是否已注册。
func IsSupported(channel string) bool {
	for _, c := range Channels() {
		if c == channel {
			return true
		}
	}
	return false
}
