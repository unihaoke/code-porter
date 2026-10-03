// Package bot 承载「IM 机器人渠道」聚合根与消息值对象。
//
// 机器人是网关面向协作平台的入口：飞书 / 企业微信把群聊消息回调到网关，
// 网关把它翻译成一个 CodePorter 任务，任务完成后再把结果推送回群里。
//
// 领域层只定义「渠道」「凭据」「消息」的语义与约束，
// 具体的事件解析、加解密、HTTP 出站都属于基础设施层。
package bot

import (
	"strings"

	"github.com/codeporter/code-porter/pkg/apperr"
)

// Channel IM 渠道标识。
type Channel string

const (
	// ChannelFeishu 飞书（Lark）自定义机器人 / 自建应用。
	ChannelFeishu Channel = "feishu"
	// ChannelWecom 企业微信群机器人 / 自建应用。
	ChannelWecom Channel = "wecom"
)

// ParseChannel 解析渠道名，未知渠道返回 apperr.CodeInvalidParam。
func ParseChannel(s string) (Channel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "feishu", "lark", "飞书":
		return ChannelFeishu, nil
	case "wecom", "wechat-work", "weixin-work", "企业微信", "企微":
		return ChannelWecom, nil
	}
	return "", apperr.Wrap(apperr.CodeInvalidParam, "unsupported bot channel: "+s, nil)
}

// String 返回规范渠道名。
func (c Channel) String() string { return string(c) }

// DisplayName 返回面向界面的中文名。
func (c Channel) DisplayName() string {
	switch c {
	case ChannelFeishu:
		return "飞书"
	case ChannelWecom:
		return "企业微信"
	}
	return string(c)
}

// AllChannels 返回全部受支持的渠道。
func AllChannels() []Channel { return []Channel{ChannelFeishu, ChannelWecom} }
