// Package wecombot 实现客户端侧的企业微信「智能机器人」（API 模式，WebSocket 长连接）。
//
// 与飞书渠道不同，企业微信官方只提供协议文档与 Node/Python SDK，没有 Go SDK，
// 因此这里直接基于 gorilla/websocket 实现帧协议（仅出站连接，不监听任何端口）：
//
//  1. 连接 wss://openws.work.weixin.qq.com 后发送 aibot_subscribe 订阅帧（bot_id+secret），
//     平台回执没有 cmd，按 headers.req_id 匹配；
//  2. 入站 aibot_msg_callback 是用户消息，aibot_event_callback 是事件
//     （enter_chat / disconnected_event 等）；
//  3. 回复回调消息走 aibot_respond_msg，headers.req_id 必须透传回调 req_id，
//     msgtype=stream 时平台在同一条消息上按 stream.id 做流式刷新；
//  4. 无回调上下文的主动推送走 aibot_send_msg（body 带 chatid，不支持 stream）；
//  5. 30s 一次 ping，连续两次无 ack 判死并指数退避重连（1s→30s）。
//
// 平台硬限制（见官方文档 path/101463 等）：同一 bot 只允许一条长连接；
// 流式消息必须在 10 分钟内 finish；单帧 stream.content ≤ 20480 字节；
// 频控 30 条/分钟、1000 条/小时。
package wecombot

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/codeporter/code-porter/internal/application/port"
)

// 默认长连接网关地址。
const defaultWSURL = "wss://openws.work.weixin.qq.com"

// 帧 cmd 常量。
const (
	cmdSubscribe     = "aibot_subscribe"
	cmdMsgCallback   = "aibot_msg_callback"
	cmdEventCallback = "aibot_event_callback"
	cmdRespondMsg    = "aibot_respond_msg"
	cmdSendMsg       = "aibot_send_msg"
	cmdPing          = "ping"
)

// 平台事件类型（cmd=aibot_event_callback 的 body.event_type）。
const (
	eventEnterChat    = "enter_chat"
	eventDisconnected = "disconnected_event"
	eventTemplateCard = "template_card_event"
	eventFeedback     = "feedback_event"
)

// frame 是企业微信长连接协议的统一信封。
// 出站与入站都长这样：cmd 标识动作，headers.req_id 关联请求/回执/回调，
// body 为各 cmd 自定义的 JSON 对象，errcode/errmsg 出现在回执中。
type frame struct {
	Cmd     string            `json:"cmd,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	ErrCode int               `json:"errcode,omitempty"`
	ErrMsg  string            `json:"errmsg,omitempty"`
}

func (f frame) reqID() string {
	if f.Headers == nil {
		return ""
	}
	return f.Headers["req_id"]
}

// ---- 出站 body ----

type subscribeBody struct {
	BotID  string `json:"bot_id"`
	Secret string `json:"secret"`
}

type textContent struct {
	Content string `json:"content"`
}

type markdownContent struct {
	Content string `json:"content"`
}

type streamContent struct {
	ID      string `json:"id"`
	Finish  bool   `json:"finish"`
	Content string `json:"content"`
}

type respondStreamBody struct {
	MsgType  string           `json:"msgtype"`
	Stream   *streamContent   `json:"stream,omitempty"`
	Text     *textContent     `json:"text,omitempty"`
	Markdown *markdownContent `json:"markdown,omitempty"`
}

// sendMsgBody 主动推送：必须显式带 chatid（无 req_id 关联的回调上下文）。
type sendMsgBody struct {
	ChatID   string           `json:"chatid"`
	MsgType  string           `json:"msgtype"`
	Text     *textContent     `json:"text,omitempty"`
	Markdown *markdownContent `json:"markdown,omitempty"`
}

// ---- 入站 body ----

type incomingMsgBody struct {
	MsgID    string `json:"msgid"`
	AIBotID  string `json:"aibotid"`
	ChatID   string `json:"chatid"`
	ChatType string `json:"chattype"` // single | group
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string      `json:"msgtype"`
	Text    textContent `json:"text"`
}

type incomingEventBody struct {
	AIBotID   string `json:"aibotid"`
	EventType string `json:"event_type"`
}

func mustRawJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		// 这些 body 都是本包内可控的简单结构，序列化失败只能是编程错误。
		panic(fmt.Errorf("wecombot: marshal frame body: %w", err))
	}
	return raw
}

func newFrame(cmd, reqID string, body any) frame {
	f := frame{Cmd: cmd, Headers: map[string]string{"req_id": reqID}}
	if body != nil {
		f.Body = mustRawJSON(body)
	}
	return f
}

// newSubscribeFrame 订阅帧：连接建立后的第一帧，平台据此完成身份认证。
func newSubscribeFrame(botID, secret, reqID string) frame {
	return newFrame(cmdSubscribe, reqID, subscribeBody{BotID: botID, Secret: secret})
}

// newPingFrame 心跳帧；回执无 cmd、按 req_id 匹配。
func newPingFrame(reqID string) frame {
	return newFrame(cmdPing, reqID, nil)
}

// newStreamFrame 流式回复帧。reqID 必须是回调帧的 req_id；
// stream.id 在同一轮回复内保持不变，finish=true 为终态。
func newStreamFrame(reqID, streamID string, finish bool, content string) frame {
	return newFrame(cmdRespondMsg, reqID, respondStreamBody{
		MsgType: "stream",
		Stream:  &streamContent{ID: streamID, Finish: finish, Content: content},
	})
}

// newRespondTextFrame / newRespondMarkdownFrame 在回调上下文内回复普通消息。
func newRespondTextFrame(reqID, content string) frame {
	return newFrame(cmdRespondMsg, reqID, respondStreamBody{
		MsgType: "text", Text: &textContent{Content: content},
	})
}

func newRespondMarkdownFrame(reqID, content string) frame {
	return newFrame(cmdRespondMsg, reqID, respondStreamBody{
		MsgType: "markdown", Markdown: &markdownContent{Content: content},
	})
}

// newSendTextFrame / newSendMarkdownFrame 主动推送（无回调 req_id，body 带 chatid）。
func newSendTextFrame(chatID, content string) frame {
	f := newFrame(cmdSendMsg, "", sendMsgBody{
		ChatID: chatID, MsgType: "text", Text: &textContent{Content: content},
	})
	return f
}

func newSendMarkdownFrame(chatID, content string) frame {
	return newFrame(cmdSendMsg, "", sendMsgBody{
		ChatID: chatID, MsgType: "markdown", Markdown: &markdownContent{Content: content},
	})
}

// mentionPrefix 群聊文本形如「@CodePorter 你好」：平台不提供结构化 mentions，
// @名是纯文本前缀，只剥离开头第一个 @token（正文里的 @人 不能误伤）。
var mentionPrefix = regexp.MustCompile(`^\s*@[^\s@]+[\s　]*`)

// convertMessage 把 aibot_msg_callback 帧翻译成标准消息；非文本返回 nil。
//
// 会话 ID 规则（平台推送模型决定）：单聊主动回复时 chatid 取发送者 userid，
// 群聊取回调 chatid。ReplyToken 透传 req_id，流式回复必须带它。
func convertMessage(f frame) (*port.IMBotMessage, error) {
	if f.Cmd != cmdMsgCallback {
		return nil, fmt.Errorf("wecombot: not a message callback frame: cmd=%s", f.Cmd)
	}
	var b incomingMsgBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		return nil, fmt.Errorf("wecombot: decode message body: %w", err)
	}
	if b.MsgType != "text" {
		return nil, nil
	}
	group := b.ChatType == "group"
	chatID := b.ChatID
	if !group {
		// 单聊：以发送者 userid 作为会话标识。
		chatID = b.From.UserID
	}
	if strings.TrimSpace(chatID) == "" {
		return nil, errors.New("wecombot: callback frame has no chat target")
	}
	text := b.Text.Content
	if group {
		text = mentionPrefix.ReplaceAllString(text, "")
	}
	return &port.IMBotMessage{
		EventID:    b.MsgID,
		ChatID:     strings.TrimSpace(chatID),
		ChatType:   chatType(group),
		SenderID:   b.From.UserID,
		Text:       strings.TrimSpace(text),
		RawText:    b.Text.Content,
		Mentioned:  group, // 群聊仅在 @机器人 时才会推送
		ReplyToken: f.reqID(),
	}, nil
}

// eventType 解析 aibot_event_callback 的事件类型；非事件帧返回空串。
func eventType(f frame) string {
	if f.Cmd != cmdEventCallback {
		return ""
	}
	var b incomingEventBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		return ""
	}
	return b.EventType
}

func chatType(group bool) port.ChatType {
	if group {
		return port.ChatGroup
	}
	return port.ChatP2P
}
