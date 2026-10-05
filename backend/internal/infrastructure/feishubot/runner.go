// Package feishubot 实现客户端侧的飞书企业自建应用机器人。
//
// 采用飞书官方 Go SDK（oapi-sdk-go/v3）的长连接模式：
// 出站 WebSocket 接收 im.message.receive_v1 事件，无需公网域名、回调验签与 Encrypt Key；
// 出站回复走 im/v1 OpenAPI（tenant_access_token 由 SDK 自动获取与刷新）。
package feishubot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/codeporter/code-porter/internal/application/port"
)

// feishuBaseURL 飞书国内版开放平台根地址（国际版为 https://open.larksuite.com）。
const feishuBaseURL = "https://open.feishu.cn"

// Runner 飞书长连接机器人，实现 port.IMBotRunner。
type Runner struct {
	appID     string
	appSecret string
	onMessage port.IMMessageHandler
	log       port.Logger
	http      *http.Client

	openAPI *lark.Client // OpenAPI 调用复用一个 client（token 自动刷新）
}

// NewRunner 构造 runner。
func NewRunner(appID, appSecret string, onMessage port.IMMessageHandler, log port.Logger) (*Runner, error) {
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
		return nil, errors.New("feishubot: app_id and app_secret are required")
	}
	if onMessage == nil {
		return nil, errors.New("feishubot: onMessage callback is required")
	}
	return &Runner{
		appID:     strings.TrimSpace(appID),
		appSecret: strings.TrimSpace(appSecret),
		onMessage: onMessage,
		log:       log.With(port.F("cmp", "feishubot")),
		http:      &http.Client{Timeout: 15 * time.Second},
		openAPI:   lark.NewClient(appID, appSecret),
	}, nil
}

// AppID 返回应用 ID（日志/状态展示用）。
func (r *Runner) AppID() string { return r.appID }

// Start 建立长连接并阻塞运行。SDK 负责心跳与日常断线重连；
// 若 Start 整体退出（如凭据错误），本方法按指数退避重新拉起，直到 ctx 取消。
func (r *Runner) Start(ctx context.Context) error {
	attempt := 0
	for ctx.Err() == nil {
		eventHandler := dispatcher.NewEventDispatcher("", "").
			OnP2MessageReceiveV1(func(c context.Context, ev *larkim.P2MessageReceiveV1) error {
				return r.dispatch(ev)
			})
		cli := larkws.NewClient(r.appID, r.appSecret,
			larkws.WithEventHandler(eventHandler),
			larkws.WithAutoReconnect(true),
			larkws.WithLogLevel(larkcore.LogLevelInfo),
		)
		start := time.Now()
		err := cli.Start(ctx)
		if ctx.Err() != nil {
			r.log.Info("feishu bot stopped")
			return nil
		}
		wait := backoff(attempt)
		r.log.Warn("feishu long connection exited, reconnecting with backoff",
			port.F("lived_ms", time.Since(start).Milliseconds()),
			port.F("wait", wait.String()), port.F("err", errString(err)))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		attempt++
	}
	return nil
}

// dispatch 把 SDK 事件翻译成标准消息并回调应用层；非文本消息忽略。
func (r *Runner) dispatch(ev *larkim.P2MessageReceiveV1) error {
	msg := convertEvent(ev)
	if msg == nil {
		return nil
	}
	r.log.Info("feishu message received",
		port.F("app_id", r.appID), port.F("message_id", msg.EventID),
		port.F("chat_type", string(msg.ChatType)), port.F("mentioned", msg.Mentioned),
		port.F("text_preview", truncate(msg.Text, 40)))
	// 回调必须在飞书 3s 窗口内返回；应用层受理后自行异步执行。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.onMessage(ctx, *msg)
}

// SendText 发送纯文本消息。
func (r *Runner) SendText(ctx context.Context, target port.IMReplyTarget, text string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	return r.createMessage(ctx, target.ChatID, larkim.MsgTypeText, string(content), nil)
}

// SendCard 发送交互卡片（标题 + lark_md 正文）。
func (r *Runner) SendCard(ctx context.Context, target port.IMReplyTarget, title, markdown string) error {
	raw, err := json.Marshal(cardContent(title, markdown))
	if err != nil {
		return err
	}
	return r.createMessage(ctx, target.ChatID, larkim.MsgTypeInteractive, string(raw), nil)
}

func (r *Runner) createMessage(ctx context.Context, chatID, msgType, content string, outMessageID *string) error {
	if strings.TrimSpace(chatID) == "" {
		return errors.New("feishubot: chat_id is empty")
	}
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()
	resp, err := r.openAPI.Im.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishubot: openapi call failed: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishubot: openapi rejected: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if outMessageID != nil && resp.Data != nil && resp.Data.MessageId != nil {
		*outMessageID = *resp.Data.MessageId
	}
	return nil
}

// cardContent 构造一次性交互卡片 JSON（schema 1.0；仅供 SendCard 兜底通知，
// 实时流式输出统一走 stream_card.go 的 schema 2.0 卡片）。
func cardContent(title, markdown string) map[string]any {
	if strings.TrimSpace(title) == "" {
		title = "CodePorter"
	}
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true, "update_multi": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": title},
		},
		"elements": []map[string]any{
			{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": markdown}},
		},
	}
}

// TestCredentials 不建长连接、不订阅事件，仅用 App ID/Secret 换取一次
// tenant_access_token，验证凭证是否正确（该接口不依赖任何额外权限点）。
func (r *Runner) TestCredentials(ctx context.Context) (port.IMBotCredentialTest, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id": r.appID, "app_secret": r.appSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		feishuBaseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return port.IMBotCredentialTest{
			OK: false, Detail: fmt.Sprintf("构造请求失败：%v", err),
		}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := r.http.Do(req)
	if err != nil {
		return port.IMBotCredentialTest{
			OK: false, Detail: fmt.Sprintf("请求飞书失败（检查网络/代理）：%v", err),
		}, fmt.Errorf("feishubot: token request: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
		TenantKey         string `json:"tenant_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return port.IMBotCredentialTest{
			OK: false, Detail: fmt.Sprintf("解析飞书响应失败：%v", err),
		}, fmt.Errorf("feishubot: decode token response: %w", err)
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return port.IMBotCredentialTest{
			OK:     false,
			Detail: fmt.Sprintf("飞书拒绝凭证：code=%d msg=%s（请核对 App ID / App Secret）", out.Code, out.Msg),
		}, errors.New(out.Msg)
	}
	extra := map[string]string{}
	if out.TenantKey != "" {
		extra["tenant_key"] = out.TenantKey
	}
	return port.IMBotCredentialTest{
		OK: true, ExpireSeconds: out.Expire, Extra: extra,
		Detail: "凭证有效，已成功获取 tenant_access_token",
	}, nil
}

// convertEvent SDK v2 消息事件 → 标准消息；非文本返回 nil。
func convertEvent(ev *larkim.P2MessageReceiveV1) *port.IMBotMessage {
	if ev == nil || ev.Event == nil || ev.Event.Message == nil {
		return nil
	}
	m := ev.Event.Message
	if strVal(m.MessageType) != "text" {
		return nil
	}
	text, ok := parseTextContent(strVal(m.Content))
	if !ok {
		return nil
	}
	mentionKeys := make([]string, 0, len(m.Mentions))
	for _, mention := range m.Mentions {
		if mention != nil && mention.Key != nil {
			mentionKeys = append(mentionKeys, *mention.Key)
		}
	}
	chatType := port.ChatP2P
	if strVal(m.ChatType) == "group" {
		chatType = port.ChatGroup
	}
	senderID := ""
	if ev.Event.Sender != nil && ev.Event.Sender.SenderId != nil {
		senderID = strVal(ev.Event.Sender.SenderId.OpenId)
	}
	return &port.IMBotMessage{
		EventID:   strVal(m.MessageId),
		ChatID:    strVal(m.ChatId),
		ChatType:  chatType,
		SenderID:  senderID,
		Text:      stripMentionPlaceholders(text, mentionKeys),
		RawText:   text,
		Mentioned: len(m.Mentions) > 0,
	}
}

// parseTextContent 解析文本消息 content（{"text":"..."}）。
func parseTextContent(raw string) (string, bool) {
	var c struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return "", false
	}
	return c.Text, true
}

// stripMentionPlaceholders 去掉 @_user_N 占位符。
func stripMentionPlaceholders(text string, keys []string) string {
	for _, key := range keys {
		if key != "" {
			text = strings.ReplaceAll(text, key, "")
		}
	}
	return strings.TrimSpace(text)
}

func backoff(attempt int) time.Duration {
	// 用乘法递增并提前封顶，避免大 attempt 下位移溢出。
	d := time.Second
	for i := 0; i < attempt && d < 60*time.Second; i++ {
		d *= 2
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
