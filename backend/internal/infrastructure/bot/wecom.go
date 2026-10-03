package bot

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	domainbot "github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// wecomEnvelope 企微回调信封（加密回调）。
type wecomEnvelope struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	AgentID    string   `xml:"AgentID"`
	Encrypt    string   `xml:"Encrypt"`
}

// wecomMessage 解密后的消息体。
type wecomMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgID"`
	AgentID      string   `xml:"AgentID"`
}

// VerifyWecomURL 处理企微回调地址的 GET 验证：校验签名并解密 echostr。
//
// 返回的明文串需由调用方原样写入响应体（不能包 JSON / XML）。
func VerifyWecomURL(b *domainbot.Bot, msgSignature, timestamp, nonce, echostr string) (string, error) {
	if b.Token() == "" || b.AESKey() == "" {
		return "", apperr.New(apperr.CodeInvalidParam,
			"wecom callback requires both token and encoding aes key")
	}
	if wecomSign(b.Token(), timestamp, nonce, echostr) != msgSignature {
		return "", apperr.Wrap(apperr.CodeUnauthorized, "wecom callback signature mismatch", ErrBadSignature)
	}
	plain, err := wecomDecrypt(b.AESKey(), echostr, "")
	if err != nil {
		return "", apperr.Wrap(apperr.CodeInvalidParam, "decrypt wecom echostr failed", err)
	}
	return string(plain), nil
}

// ParseWecom 解析企微消息回调。
//
// 入参中的 msgSignature / timestamp / nonce 来自回调 URL 查询串。
// 返回 nil 表示事件无需处理（如非文本消息、事件类消息）。
func ParseWecom(
	b *domainbot.Bot,
	body []byte,
	msgSignature, timestamp, nonce string,
) (*domainbot.InboundMessage, error) {
	if b.Token() == "" || b.AESKey() == "" {
		return nil, apperr.New(apperr.CodeInvalidParam,
			"wecom callback requires both token and encoding aes key")
	}

	var env wecomEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "invalid wecom callback xml", err)
	}
	if env.Encrypt == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "wecom callback missing encrypt field")
	}
	if wecomSign(b.Token(), timestamp, nonce, env.Encrypt) != msgSignature {
		return nil, apperr.Wrap(apperr.CodeUnauthorized, "wecom callback signature mismatch", ErrBadSignature)
	}
	plain, err := wecomDecrypt(b.AESKey(), env.Encrypt, "")
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "decrypt wecom message failed", err)
	}

	var m wecomMessage
	if err := xml.Unmarshal(plain, &m); err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "invalid wecom message xml", err)
	}
	if m.MsgType != "text" {
		return nil, nil
	}

	// 企微单聊消息 Content 为纯文本；群聊中 @机器人 会带 @昵称 前缀，这里做最小清理。
	text := strings.TrimSpace(m.Content)

	return &domainbot.InboundMessage{
		BotID:      b.ID(),
		Channel:    domainbot.ChannelWecom,
		EventID:    m.MsgID,
		ChatID:     m.FromUserName,
		ChatType:   domainbot.ChatGroup,
		SenderID:   m.FromUserName,
		Text:       text,
		RawText:    m.Content,
		Mentioned:  true,
		ReceivedAt: time.Now(),
	}, nil
}

// sendWecom 通过企微群机器人 Webhook 推送消息。
func sendWecom(ctx context.Context, client *http.Client, b *domainbot.Bot, msg domainbot.OutboundMessage) error {
	payload := map[string]any{}
	switch msg.Format {
	case domainbot.FormatText:
		payload["msgtype"] = "text"
		payload["text"] = map[string]any{"content": msg.Content}
	default:
		payload["msgtype"] = "markdown"
		payload["markdown"] = map[string]any{"content": msg.Content}
	}
	return postJSON(ctx, client, b.WebhookURL(), payload)
}
