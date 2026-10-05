package wecombot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/application/port"
)

// mockWSServer 企微长连接网关的本地模拟：
//   - 每个连接的订阅帧立即回 errcode 回执；
//   - 收到的所有帧记录到 frames 并投入 frameCh；
//   - autoAckPing 时对 ping 帧自动回执；
//   - 测试可通过 pushFrame 向最近连接推送回调/事件帧。
type mockWSServer struct {
	t           *testing.T
	srv         *httptest.Server
	subAckErr   int  // 订阅回执 errcode（0=成功）
	autoAckPing bool // 是否自动回执 ping

	writeMu  sync.Mutex
	dataMu   sync.Mutex
	frames   []frame
	lastConn *websocket.Conn

	connections int32
	frameCh     chan frame
	connectedCh chan int32
}

func startMock(t *testing.T) *mockWSServer {
	m := &mockWSServer{
		t:           t,
		frameCh:     make(chan frame, 128),
		connectedCh: make(chan int32, 8),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		n := atomic.AddInt32(&m.connections, 1)
		m.dataMu.Lock()
		m.lastConn = conn
		m.dataMu.Unlock()
		select {
		case m.connectedCh <- n:
		default:
		}
		defer conn.Close()

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var f frame
			if json.Unmarshal(raw, &f) != nil {
				continue
			}
			m.dataMu.Lock()
			m.frames = append(m.frames, f)
			m.dataMu.Unlock()
			select {
			case m.frameCh <- f:
			default:
			}
			// 订阅回执：无 cmd，按 req_id 匹配。
			if f.Cmd == cmdSubscribe {
				m.sendTo(conn, frame{
					Headers: map[string]string{"req_id": f.reqID()},
					ErrCode: m.subAckErr,
				})
			}
			if m.autoAckPing && f.Cmd == cmdPing {
				m.sendTo(conn, frame{Headers: map[string]string{"req_id": f.reqID()}})
			}
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockWSServer) wsURL() string {
	return "ws://" + m.srv.Listener.Addr().String()
}

// sendTo 向指定连接写一帧（加锁，避免与服务端其他写并发）。
func (m *mockWSServer) sendTo(conn *websocket.Conn, f frame) {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	_ = conn.WriteJSON(f)
}

// pushFrame 测试侧主动向最近建立的连接推帧。
func pushFrame(t *testing.T, m *mockWSServer, f frame) {
	t.Helper()
	m.dataMu.Lock()
	conn := m.lastConn
	m.dataMu.Unlock()
	if conn == nil {
		t.Fatal("mock server has no connection yet")
	}
	m.sendTo(conn, f)
}

// waitFrames 阻塞直到收到至少 n 帧或超时。
func (m *mockWSServer) waitFrames(n int, timeout time.Duration) []frame {
	deadline := time.After(timeout)
	got := 0
	for {
		select {
		case <-m.frameCh:
			got++
			if got >= n {
				return m.snapshotFrames()
			}
		case <-deadline:
			t := m.snapshotFrames()
			if len(t) < n {
				m.t.Fatalf("waitFrames: want %d, got %d", n, len(t))
			}
			return t
		}
	}
}

func (m *mockWSServer) snapshotFrames() []frame {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	out := make([]frame, len(m.frames))
	copy(out, m.frames)
	return out
}

// findStreamFinish 在收到的帧中找出 finish=true 的流式帧。
func findStreamFinish(t *testing.T, frames []frame) (streamID, reqID string) {
	t.Helper()
	for _, f := range frames {
		if f.Cmd != cmdRespondMsg {
			continue
		}
		var b respondStreamBody
		if json.Unmarshal(f.Body, &b) != nil || b.Stream == nil {
			continue
		}
		if b.Stream.Finish {
			return b.Stream.ID, f.reqID()
		}
	}
	t.Fatal("no finished stream frame observed")
	return "", ""
}

// newTestRunner 构造指向模拟服务器的 runner。
func newTestRunner(t *testing.T, m *mockWSServer, onMessage port.IMMessageHandler, opts ...Option) *Runner {
	t.Helper()
	all := append([]Option{WithWSURL(m.wsURL())}, opts...)
	r, err := NewRunner("bot_test", "sec_test", onMessage, port.NopLogger{}, all...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// startRunner 后台启动 Start 并等待模拟服务器收到一次连接。
func startRunner(t *testing.T, r *Runner, m *mockWSServer) (context.CancelFunc, <-chan int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Start(ctx); close(done) }()
	select {
	case n := <-m.connectedCh:
		_ = n
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("runner never connected")
	}
	return cancel, m.connectedCh
}

// TestStreamSequence 订阅 → 回调 → 流式建卡 → 终态 finish，req_id 全程透传。
func TestStreamSequence(t *testing.T) {
	var runner *Runner
	m := startMock(t)
	onMessage := func(ctx context.Context, msg port.IMBotMessage) error {
		id, err := runner.OpenStreamCard(ctx, port.IMReplyTarget{
			ChatID: msg.ChatID, ReplyToken: msg.ReplyToken,
		}, port.IMCardState{Phase: port.IMCardRunning, Summary: "思考中"})
		if err != nil {
			t.Errorf("open stream: %v", err)
			return nil
		}
		if err := runner.UpdateStreamCard(ctx, id, port.IMCardState{
			Phase: port.IMCardDone, Body: "最终答案 42", Summary: "已完成",
		}); err != nil {
			t.Errorf("update stream: %v", err)
		}
		return nil
	}
	runner = newTestRunner(t, m, onMessage)
	cancel, _ := startRunner(t, runner, m)
	defer cancel()

	// 等订阅帧到达后，由服务器推送一条单聊文本回调。
	m.waitFrames(1, time.Second)
	body := incomingMsgBody{MsgID: "msg_1", ChatType: "single", MsgType: "text"}
	body.From.UserID = "u_1"
	body.Text.Content = "你好"
	raw, _ := json.Marshal(body)
	pushFrame(t, m, frame{
		Cmd: cmdMsgCallback, Headers: map[string]string{"req_id": "cb-1"}, Body: raw,
	})

	// 订阅 + 首流帧 + 终态帧。
	frames := m.waitFrames(3, 2*time.Second)
	var streamIDs, reqIDs []string
	firstStreamFinish := true
	for _, f := range frames {
		if f.Cmd != cmdRespondMsg {
			continue
		}
		var b respondStreamBody
		if json.Unmarshal(f.Body, &b) != nil || b.Stream == nil {
			t.Fatalf("stream frame body wrong: %s", string(f.Body))
		}
		streamIDs = append(streamIDs, b.Stream.ID)
		reqIDs = append(reqIDs, f.reqID())
		if len(streamIDs) == 1 {
			firstStreamFinish = b.Stream.Finish
		}
		if strings.Contains(b.Stream.Content, "最终答案") && !b.Stream.Finish {
			t.Fatal("final content must be sent with finish=true")
		}
	}
	if len(streamIDs) != 2 || streamIDs[0] != streamIDs[1] {
		t.Fatalf("stream frames must reuse one id, got %v", streamIDs)
	}
	if firstStreamFinish {
		t.Fatal("first stream frame must be finish=false")
	}
	if reqIDs[0] != "cb-1" || reqIDs[1] != "cb-1" {
		t.Fatalf("stream frames must carry callback req_id, got %v", reqIDs)
	}
	finishID, finishReq := findStreamFinish(t, m.snapshotFrames())
	if finishID != streamIDs[0] || finishReq != "cb-1" {
		t.Fatalf("finish frame mismatch: id=%s req=%s", finishID, finishReq)
	}
}

// TestTestCredentials 订阅成功 → 凭证有效；errcode 非 0 → 拒绝。
func TestTestCredentials(t *testing.T) {
	m := startMock(t)
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil })
	res, err := r.TestCredentials(context.Background())
	if err != nil || !res.OK {
		t.Fatalf("valid credentials must pass: ok=%v err=%v detail=%s", res.OK, err, res.Detail)
	}

	bad := startMock(t)
	bad.subAckErr = 60011
	rb := newTestRunner(t, bad, func(context.Context, port.IMBotMessage) error { return nil })
	res2, err2 := rb.TestCredentials(context.Background())
	if err2 == nil || res2.OK {
		t.Fatalf("rejected credentials must fail: %+v", res2)
	}
	if !strings.Contains(res2.Detail, "60011") {
		t.Fatalf("detail must expose platform errcode: %s", res2.Detail)
	}
}

// TestHeartbeatAcked ping 被正常回执时不应触发重连（订阅只有一次）。
func TestHeartbeatAcked(t *testing.T) {
	m := startMock(t)
	m.autoAckPing = true
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil },
		withTimings(timings{heartbeat: 20 * time.Millisecond}))

	cancel, _ := startRunner(t, r, m)
	defer cancel()

	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&m.connections); got != 1 {
		t.Fatalf("acked heartbeats must not cause reconnect, connections=%d", got)
	}
	pings := 0
	for _, f := range m.snapshotFrames() {
		if f.Cmd == cmdPing {
			pings++
		}
	}
	if pings < 3 {
		t.Fatalf("expected several pings, got %d", pings)
	}
}

// TestDisconnectedEventReconnect 收到 disconnected_event 后应主动重连。
func TestDisconnectedEventReconnect(t *testing.T) {
	m := startMock(t)
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil },
		withTimings(timings{heartbeat: time.Hour}))

	cancel, connectedCh := startRunner(t, r, m)
	defer cancel()
	m.waitFrames(1, time.Second) // 订阅已发

	ev, _ := json.Marshal(incomingEventBody{AIBotID: "bot_test", EventType: eventDisconnected})
	pushFrame(t, m, frame{Cmd: cmdEventCallback, Headers: map[string]string{"req_id": "ev-1"}, Body: ev})

	select {
	case n := <-connectedCh:
		if n < 2 {
			t.Fatalf("expected reconnect, connections=%d", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not reconnect after disconnected_event")
	}
}

// TestOpenStreamCardRequiresReplyToken 企微流式回复必须携带回调 req_id：
// 无 ReplyToken 时在写连接之前直接拒绝（不依赖连接是否就绪）。
func TestOpenStreamCardRequiresReplyToken(t *testing.T) {
	m := startMock(t)
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil })
	// 故意不启动 Start：若校验先于连接写入，此处不应 panic / 阻塞。
	if _, err := r.OpenStreamCard(context.Background(), port.IMReplyTarget{ChatID: "u_1"},
		port.IMCardState{Phase: port.IMCardRunning, Summary: "思考中"}); err == nil ||
		!strings.Contains(err.Error(), "req_id") {
		t.Fatalf("missing ReplyToken must be rejected with req_id hint, got %v", err)
	}
	if err := r.UpdateStreamCard(context.Background(), "wec_never",
		port.IMCardState{Phase: port.IMCardDone, Body: "x"}); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown stream id must be rejected, got %v", err)
	}
}

// TestTerminalFrameWriteFailureDropsStream 终态帧写入失败时也必须注销条目，
// 避免 streams map 随失败对话无界增长。
func TestTerminalFrameWriteFailureDropsStream(t *testing.T) {
	m := startMock(t)
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil })
	// 未建立连接（conn=nil）：write 必然失败。
	r.streams["wec_dead"] = &streamSession{reqID: "cb-dead", chatID: "u_1"}
	err := r.UpdateStreamCard(context.Background(), "wec_dead",
		port.IMCardState{Phase: port.IMCardFailed, Body: "❌ 失败"})
	if err == nil {
		t.Fatal("terminal write without connection must fail")
	}
	if _, ok := r.streams["wec_dead"]; ok {
		t.Fatal("terminal stream entry must be dropped even when write fails")
	}
}

// TestProactiveSendWithoutReqID 无回调上下文走 aibot_send_msg 且不带 req_id。
func TestProactiveSendWithoutReqID(t *testing.T) {
	m := startMock(t)
	r := newTestRunner(t, m, func(context.Context, port.IMBotMessage) error { return nil })
	cancel, _ := startRunner(t, r, m)
	defer cancel()

	m.waitFrames(1, time.Second) // 订阅成功后再发，避免 conn 未就绪
	ctx := context.Background()
	if err := r.SendText(ctx, port.IMReplyTarget{ChatID: "oc_g"}, "主动消息"); err != nil {
		t.Fatal(err)
	}
	if err := r.SendCard(ctx, port.IMReplyTarget{ChatID: "oc_g"}, "T", "内容"); err != nil {
		t.Fatal(err)
	}
	frames := m.waitFrames(3, time.Second)
	var sawText, sawMD bool
	for _, f := range frames {
		if f.Cmd != cmdSendMsg || f.reqID() != "" {
			continue
		}
		var b sendMsgBody
		if json.Unmarshal(f.Body, &b) != nil {
			t.Fatalf("send body invalid: %s", string(f.Body))
		}
		if b.ChatID != "oc_g" {
			t.Fatalf("send frame must carry chatid, got %s", b.ChatID)
		}
		if b.MsgType == "text" {
			sawText = true
		}
		if b.MsgType == "markdown" {
			sawMD = true
		}
	}
	if !sawText || !sawMD {
		t.Fatalf("proactive text/markdown frames missing: text=%v md=%v", sawText, sawMD)
	}
}
