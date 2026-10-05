package wecombot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/pkg/id"
)

// 平台节拍与连接参数（见包文档中的硬限制）。
const (
	flushInterval    = 2 * time.Second  // 流式帧刷新节流（应用层 cardStream 读取）
	maxLifetime      = 10 * time.Minute // 流式消息必须在 10 分钟内 finish
	heartbeatEvery   = 30 * time.Second
	missedPingLimit  = 2 // 连续 2 次心跳无 ack 判死
	subscribeTimeout = 10 * time.Second
	credTestTimeout  = 12 * time.Second
	dialTimeout      = 15 * time.Second
)

// errReplaced 收到 disconnected_event：本连接被同一 bot 的新连接踢下线。
var errReplaced = errors.New("wecombot: connection replaced by another instance (disconnected_event)")

// timings 连接循环的可调时序（生产用常量，测试可压缩）。
type timings struct {
	heartbeat time.Duration
}

// wsConn 抽象一条 WebSocket 连接，便于在测试中注入模拟服务器上的连接。
type wsConn interface {
	writeFrame(ctx context.Context, f frame) error
	readFrame(ctx context.Context) (frame, error)
	close() error
}

// DialFunc 拨号器抽象。
type DialFunc func(ctx context.Context, url string) (wsConn, error)

// Option Runner 的可选构造参数（主要给测试覆盖网关地址与时序）。
type Option func(*Runner)

// WithWSURL 覆盖长连接地址。
func WithWSURL(url string) Option {
	return func(r *Runner) { r.url = strings.TrimSpace(url) }
}

// withTimings 压缩心跳间隔，仅供包内测试。
func withTimings(t timings) Option {
	return func(r *Runner) { r.timings = t }
}

// streamSession 一条进行中的流式回复：stream.id → 回调 req_id + 会话。
type streamSession struct {
	reqID  string
	chatID string
}

// Runner 企业微信长连接机器人，实现 port.IMBotRunner / port.IMBotCredentialTester
// / port.IMBotCardPacer。
type Runner struct {
	botID     string
	secret    string
	url       string
	onMessage port.IMMessageHandler
	log       port.Logger
	dial      DialFunc
	timings   timings

	// connMu 保护当前连接（重连会整体替换）。
	connMu sync.Mutex
	conn   wsConn

	// streams 登记进行中的流式回复；终态帧发出后删除。
	streamsMu sync.Mutex
	streams   map[string]*streamSession
}

// NewRunner 构造 runner。
func NewRunner(botID, botSecret string, onMessage port.IMMessageHandler, log port.Logger, opts ...Option) (*Runner, error) {
	if strings.TrimSpace(botID) == "" || strings.TrimSpace(botSecret) == "" {
		return nil, errors.New("wecombot: bot_id and secret are required")
	}
	if onMessage == nil {
		return nil, errors.New("wecombot: onMessage callback is required")
	}
	r := &Runner{
		botID:     strings.TrimSpace(botID),
		secret:    strings.TrimSpace(botSecret),
		url:       defaultWSURL,
		onMessage: onMessage,
		log:       log.With(port.F("cmp", "wecombot")),
		dial:      defaultDial,
		timings:   timings{heartbeat: heartbeatEvery},
		streams:   make(map[string]*streamSession),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// BotID 返回机器人 ID（日志/状态展示用）。
func (r *Runner) BotID() string { return r.botID }

// CardFlushInterval / MaxCardLifetime 实现 port.IMBotCardPacer。
func (r *Runner) CardFlushInterval() time.Duration { return flushInterval }
func (r *Runner) MaxCardLifetime() time.Duration   { return maxLifetime }

// Start 建立长连接并阻塞运行，直到 ctx 取消；断线/被踢按指数退避重连（1s→30s）。
func (r *Runner) Start(ctx context.Context) error {
	attempt := 0
	for ctx.Err() == nil {
		start := time.Now()
		err := r.runSession(ctx)
		if ctx.Err() != nil {
			r.log.Info("wecom bot stopped")
			return nil
		}
		wait := backoff(attempt)
		r.log.Warn("wecom long connection exited, reconnecting with backoff",
			port.F("bot_id", r.botID),
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

// runSession 建立一次连接：拨号 → 订阅 → 读循环 + 心跳，返回连接级错误。
func (r *Runner) runSession(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, err := r.dial(dialCtx, r.url)
	cancel()
	if err != nil {
		return fmt.Errorf("wecombot: dial: %w", err)
	}
	r.setConn(conn)
	defer func() {
		r.clearConn(conn)
		_ = conn.close()
	}()

	acks := newAckTable()
	defer acks.closeAll()

	// 读泵 + 回执路由必须在订阅之前就位：订阅回执本身是一条入站帧，
	// 若先等回执再读 socket，会把自己永久卡死在 subscribe timeout。
	// 读泵只做两件事：无 cmd 的回执按 req_id 派发（ping 回执另发信号），
	// 有 cmd 的业务帧投递到 incoming。
	incoming := make(chan frame, 16)
	readErr := make(chan error, 1)
	pingAcked := make(chan struct{}, 1)
	go func() {
		for {
			f, err := conn.readFrame(ctx)
			if err != nil {
				select {
				case readErr <- err:
				case <-ctx.Done():
				}
				return
			}
			if f.Cmd == "" {
				if strings.HasPrefix(f.reqID(), "weping_") {
					select {
					case pingAcked <- struct{}{}:
					default:
					}
				}
				var ackErr error
				if f.ErrCode != 0 {
					ackErr = fmt.Errorf("errcode=%d errmsg=%s", f.ErrCode, f.ErrMsg)
				}
				acks.resolve(f.reqID(), ackErr)
				continue
			}
			select {
			case incoming <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	// 订阅（认证）：回执无 cmd，按 req_id 匹配，10s 超时。
	subReqID := id.New("wesub_")
	ackCh := acks.register(subReqID)
	if err := conn.writeFrame(ctx, newSubscribeFrame(r.botID, r.secret, subReqID)); err != nil {
		return fmt.Errorf("wecombot: send subscribe: %w", err)
	}
	select {
	case ackErr, ok := <-ackCh:
		if !ok {
			return errors.New("wecombot: connection closed before subscribe receipt")
		}
		if ackErr != nil {
			return fmt.Errorf("wecombot: subscribe rejected: %w", ackErr)
		}
	case err := <-readErr:
		return fmt.Errorf("wecombot: read loop before subscribe ack: %w", err)
	case <-time.After(subscribeTimeout):
		return errors.New("wecombot: subscribe timeout (no receipt within 10s)")
	case <-ctx.Done():
		return nil
	}
	r.log.Info("wecom bot subscribed", port.F("bot_id", r.botID))
	// 新会话订阅成功后，丢弃上一条连接残留的流式登记：stream 帧依赖回调
	// req_id，跨连接是否仍被平台接受没有保证；旧会话上未 finish 的回复让
	// 应用层按更新失败走兜底，避免带着陈旧 req_id 在新连接上继续发。
	r.resetStreams()

	// 心跳：每 heartbeat 发一次 ping；收到回执清零计数，发新 ping 前若上一个
	// 仍无回执则累计，连续 missedPingLimit 次即判死并进入重连。
	ticker := time.NewTicker(r.timings.heartbeat)
	defer ticker.Stop()
	pendingPingID := ""
	missed := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return fmt.Errorf("wecombot: read loop: %w", err)
		case <-pingAcked:
			pendingPingID = ""
			missed = 0
		case f := <-incoming:
			switch f.Cmd {
			case cmdMsgCallback:
				r.dispatch(f)
			case cmdEventCallback:
				if eventType(f) == eventDisconnected {
					return errReplaced
				}
				// enter_chat / template_card_event / feedback_event 暂不处理。
			case cmdPing:
				// 对端探测时原样回执（带同一 req_id）。
				_ = conn.writeFrame(ctx, newPingFrame(f.reqID()))
			default:
				// 未知 cmd 忽略，前向兼容。
			}
		case <-ticker.C:
			if pendingPingID != "" {
				missed++
				if missed >= missedPingLimit {
					return errors.New("wecombot: heartbeat dead, reconnecting")
				}
			}
			pingID := id.New("weping_")
			pendingPingID = pingID
			acks.register(pingID)
			if err := conn.writeFrame(ctx, newPingFrame(pingID)); err != nil {
				return fmt.Errorf("wecombot: send ping: %w", err)
			}
		}
	}
}

// dispatch 翻译回调消息并同步调用应用层（应用层受理后自行异步执行）。
func (r *Runner) dispatch(f frame) {
	msg, err := convertMessage(f)
	if err != nil {
		r.log.Warn("bad message callback frame", port.F("err", err.Error()))
		return
	}
	if msg == nil {
		return
	}
	r.log.Info("wecom message received",
		port.F("bot_id", r.botID), port.F("message_id", msg.EventID),
		port.F("chat_type", string(msg.ChatType)),
		port.F("text_preview", truncate(msg.Text, 40)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.onMessage(ctx, *msg); err != nil {
		r.log.Warn("onMessage returned error, platform may redeliver",
			port.F("message_id", msg.EventID), port.F("err", err.Error()))
	}
}

// ---- port.IMBotRunner 出站实现 ----

// SendText 发送纯文本：有回调凭证走 respond_msg，否则主动推送。
func (r *Runner) SendText(ctx context.Context, target port.IMReplyTarget, text string) error {
	f := newSendTextFrame(target.ChatID, text)
	if target.ReplyToken != "" {
		f = newRespondTextFrame(target.ReplyToken, text)
	}
	return r.write(f)
}

// SendCard 发送一次性 markdown 消息（用于兜底通知）。
func (r *Runner) SendCard(ctx context.Context, target port.IMReplyTarget, title, markdown string) error {
	content := markdown
	if t := strings.TrimSpace(title); t != "" {
		content = "**" + t + "**\n\n" + markdown
	}
	f := newSendMarkdownFrame(target.ChatID, content)
	if target.ReplyToken != "" {
		f = newRespondMarkdownFrame(target.ReplyToken, content)
	}
	return r.write(f)
}

// OpenStreamCard 登记一条流式回复并发送首帧（finish=false）。
// 企微流式回复必须携带回调 req_id；无回调上下文（如主动推送）不支持流式。
func (r *Runner) OpenStreamCard(ctx context.Context, target port.IMReplyTarget, state port.IMCardState) (string, error) {
	if strings.TrimSpace(target.ReplyToken) == "" {
		return "", errors.New("wecombot: streaming reply requires a callback req_id (proactive push cannot stream)")
	}
	streamID := id.New("wec_")
	r.streamsMu.Lock()
	r.streams[streamID] = &streamSession{reqID: target.ReplyToken, chatID: target.ChatID}
	r.streamsMu.Unlock()
	if err := r.write(newStreamFrame(target.ReplyToken, streamID, false, renderStream(state))); err != nil {
		r.streamsMu.Lock()
		delete(r.streams, streamID)
		r.streamsMu.Unlock()
		return "", err
	}
	return streamID, nil
}

// UpdateStreamCard 发送同 stream.id 的后续帧；终态 finish=true 并注销。
func (r *Runner) UpdateStreamCard(ctx context.Context, streamID string, state port.IMCardState) error {
	r.streamsMu.Lock()
	sess := r.streams[streamID]
	r.streamsMu.Unlock()
	if sess == nil {
		return fmt.Errorf("wecombot: unknown or finished stream id: %s", streamID)
	}
	finish := state.Phase != port.IMCardRunning
	if finish {
		// 终态帧无论写入成败都注销：失败时连接通常已失效，重试不会成功，
		// 残留条目只会让 streams map 无界增长。
		defer r.dropStream(streamID)
	}
	return r.write(newStreamFrame(sess.reqID, streamID, finish, renderStream(state)))
}

func (r *Runner) dropStream(streamID string) {
	r.streamsMu.Lock()
	delete(r.streams, streamID)
	r.streamsMu.Unlock()
}

// resetStreams 清空所有流式登记（新连接订阅成功后调用）。
func (r *Runner) resetStreams() {
	r.streamsMu.Lock()
	r.streams = make(map[string]*streamSession)
	r.streamsMu.Unlock()
}

// TestCredentials 实现 port.IMBotCredentialTester：拨号 + 订阅成功即断开。
func (r *Runner) TestCredentials(ctx context.Context) (port.IMBotCredentialTest, error) {
	dialCtx, cancel := context.WithTimeout(ctx, credTestTimeout)
	defer cancel()
	conn, err := r.dial(dialCtx, r.url)
	if err != nil {
		return port.IMBotCredentialTest{
			OK:     false,
			Detail: fmt.Sprintf("连接企业微信网关失败（检查网络/代理）：%v", err),
		}, err
	}
	defer conn.close()

	acks := newAckTable()
	defer acks.closeAll()
	reqID := id.New("wesub_")
	ackCh := acks.register(reqID)
	if err := conn.writeFrame(dialCtx, newSubscribeFrame(r.botID, r.secret, reqID)); err != nil {
		return port.IMBotCredentialTest{
			OK: false, Detail: fmt.Sprintf("发送订阅帧失败：%v", err),
		}, err
	}
	go func() {
		// 接收回执（测试/真实连接都只需要下一帧）。
		f, err := conn.readFrame(dialCtx)
		if err != nil {
			acks.resolve(reqID, err)
			return
		}
		var ackErr error
		if f.ErrCode != 0 {
			ackErr = fmt.Errorf("errcode=%d errmsg=%s", f.ErrCode, f.ErrMsg)
		}
		acks.resolve(reqID, ackErr)
	}()
	select {
	case ackErr, ok := <-ackCh:
		if !ok {
			err := errors.New("连接在收到订阅回执前关闭")
			return port.IMBotCredentialTest{OK: false, Detail: err.Error()}, err
		}
		if ackErr != nil {
			return port.IMBotCredentialTest{
				OK:     false,
				Detail: fmt.Sprintf("企业微信拒绝凭证：%v（请核对 Bot ID / Secret）", ackErr),
			}, ackErr
		}
		return port.IMBotCredentialTest{
			OK: true, Detail: "凭证有效，已成功订阅智能机器人长连接",
		}, nil
	case <-dialCtx.Done():
		return port.IMBotCredentialTest{
			OK: false, Detail: "订阅超时（12s 内未收到回执），请检查网络或 Bot ID / Secret",
		}, dialCtx.Err()
	}
}

func (r *Runner) write(f frame) error {
	r.connMu.Lock()
	conn := r.conn
	r.connMu.Unlock()
	if conn == nil {
		return errors.New("wecombot: connection is not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return conn.writeFrame(ctx, f)
}

func (r *Runner) setConn(c wsConn) {
	r.connMu.Lock()
	r.conn = c
	r.connMu.Unlock()
}

// clearConn 只在参数仍是当前连接时清空（避免旧连接退出时抹掉新连接）。
func (r *Runner) clearConn(c wsConn) {
	r.connMu.Lock()
	if r.conn == c {
		r.conn = nil
	}
	r.connMu.Unlock()
}

// ---- ack 表 ----

type ackTable struct {
	mu sync.Mutex
	m  map[string]chan error
}

func newAckTable() *ackTable {
	return &ackTable{m: make(map[string]chan error)}
}

func (t *ackTable) register(reqID string) chan error {
	ch := make(chan error, 1)
	t.mu.Lock()
	t.m[reqID] = ch
	t.mu.Unlock()
	return ch
}

func (t *ackTable) resolve(reqID string, err error) {
	t.mu.Lock()
	ch := t.m[reqID]
	delete(t.m, reqID)
	t.mu.Unlock()
	if ch != nil {
		select {
		case ch <- err:
		default:
		}
	}
}

// closeAll 连接关闭时唤醒所有等待者（关闭通道），避免 goroutine 永久等待。
func (t *ackTable) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, ch := range t.m {
		close(ch)
		delete(t.m, k)
	}
}

// ---- gorilla 适配 ----

type gorillaConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func defaultDial(ctx context.Context, url string) (wsConn, error) {
	d := websocket.Dialer{HandshakeTimeout: dialTimeout}
	c, _, err := d.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return &gorillaConn{conn: c}, nil
}

func (c *gorillaConn) writeFrame(_ context.Context, f frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(f)
}

func (c *gorillaConn) readFrame(_ context.Context) (frame, error) {
	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		return frame{}, err
	}
	var f frame
	if err := json.Unmarshal(raw, &f); err != nil {
		return frame{}, fmt.Errorf("wecombot: decode frame: %w", err)
	}
	return f, nil
}

func (c *gorillaConn) close() error {
	// 发一个正常关闭帧再关（best effort）。
	return c.conn.Close()
}

func backoff(attempt int) time.Duration {
	d := time.Second
	for i := 0; i < attempt && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
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
