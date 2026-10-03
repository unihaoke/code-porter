package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	domainbot "github.com/codeporter/code-porter/internal/domain/bot"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// feishuEventType 关注的飞书事件类型。
const feishuEventType = "im.message.receive_v1"

// feishuEnvelope 飞书回调信封（兼容 v2 schema 与旧版 url_verification）。
type feishuEnvelope struct {
	Schema    string `json:"schema"`
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Encrypt   string `json:"encrypt"`
	Header    struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event *struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
			Mentions    []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"mentions"`
		} `json:"message"`
	} `json:"event"`
}

// ParseFeishu 解析飞书回调。
//
// 返回值为（入站消息, URL 验证 challenge, 错误）：
//   - challenge 非空时表示这是飞书配置回调地址时的验证请求，调用方应原样回显；
//   - 入站消息为空且 challenge 为空时表示事件无需处理（如非文本消息、重复事件）。
func ParseFeishu(b *domainbot.Bot, body []byte) (*domainbot.InboundMessage, string, error) {
	env, err := decodeFeishuEnvelope(b, body)
	if err != nil {
		return nil, "", err
	}
	if env.Type == "url_verification" {
		return nil, env.Challenge, nil
	}

	// Token 校验（Verification Token）：配置过就必须匹配，防止任意来源投递。
	token := env.Token
	if token == "" {
		token = env.Header.Token
	}
	if b.Token() != "" && token != b.Token() {
		return nil, "", apperr.New(apperr.CodeUnauthorized, "feishu verification token mismatch")
	}

	if env.Event == nil {
		return nil, "", nil
	}
	if env.Header.EventType != "" && env.Header.EventType != feishuEventType {
		// 非「接收消息」事件（如机器人进群）直接忽略。
		return nil, "", nil
	}
	ev := env.Event
	if ev.Message.MessageType != "text" {
		return nil, "", nil
	}

	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(ev.Message.Content), &content); err != nil {
		return nil, "", apperr.Wrap(apperr.CodeInvalidParam, "invalid feishu message content", err)
	}

	raw := content.Text
	text := stripMentions(raw, ev.Message.Mentions)
	chatType := domainbot.ChatP2P
	if strings.EqualFold(ev.Message.ChatType, "group") {
		chatType = domainbot.ChatGroup
	}

	return &domainbot.InboundMessage{
		BotID:      b.ID(),
		Channel:    domainbot.ChannelFeishu,
		EventID:    ev.Message.MessageID,
		ChatID:     ev.Message.ChatID,
		ChatType:   chatType,
		SenderID:   ev.Sender.SenderID.OpenID,
		Text:       strings.TrimSpace(text),
		RawText:    raw,
		Mentioned:  len(ev.Message.Mentions) > 0,
		ReceivedAt: time.Now(),
	}, "", nil
}

// decodeFeishuEnvelope 解密（若开启加密）并反序列化信封。
func decodeFeishuEnvelope(b *domainbot.Bot, body []byte) (*feishuEnvelope, error) {
	var probe struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "invalid feishu callback body", err)
	}
	raw := body
	if probe.Encrypt != "" {
		if b.AESKey() == "" {
			return nil, apperr.New(apperr.CodeInvalidParam,
				"feishu event is encrypted but bot has no encoding aes key")
		}
		plain, err := feishuDecrypt(b.AESKey(), probe.Encrypt)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeInvalidParam, "decrypt feishu event failed", err)
		}
		raw = plain
	}
	var env feishuEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "invalid feishu event payload", err)
	}
	env.Encrypt = probe.Encrypt
	return &env, nil
}

// stripMentions 去掉文本中的 @占位符（形如 @_user_1）。
func stripMentions(text string, mentions []struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}) string {
	out := text
	for _, m := range mentions {
		if m.Key != "" {
			out = strings.ReplaceAll(out, m.Key, "")
		}
	}
	return strings.TrimSpace(out)
}

// sendFeishu 通过自定义机器人 Webhook 推送消息。
func sendFeishu(ctx context.Context, client *http.Client, b *domainbot.Bot, msg domainbot.OutboundMessage) error {
	payload := map[string]any{}
	switch msg.Format {
	case domainbot.FormatText:
		payload["msg_type"] = "text"
		payload["content"] = map[string]string{"text": msg.Content}
	default:
		title := msg.Title
		if title == "" {
			title = "CodePorter"
		}
		payload["msg_type"] = "interactive"
		payload["card"] = map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"template": "blue",
				"title":    map[string]any{"tag": "plain_text", "content": title},
			},
			"elements": []map[string]any{
				{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": msg.Content}},
			},
		}
	}
	if b.Secret() != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		payload["timestamp"] = ts
		payload["sign"] = feishuSign(b.Secret(), ts)
	}
	return postJSON(ctx, client, b.WebhookURL(), payload)
}

// postJSON 发送 JSON 请求并校验渠道返回的业务码。
func postJSON(ctx context.Context, client *http.Client, url string, payload any) error {
	if url == "" {
		return errors.New("bot: webhook url is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bot: webhook http %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Code    int    `json:"code"`
		ErrCode int    `json:"errcode"`
		Msg     string `json:"msg"`
		Errmsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// 部分渠道成功时返回纯文本 "ok"，无需解析。
		return nil
	}
	if out.Code != 0 || out.ErrCode != 0 {
		detail := out.Msg
		if detail == "" {
			detail = out.Errmsg
		}
		return fmt.Errorf("bot: webhook rejected: %s", detail)
	}
	return nil
}
