package client

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/infrastructure/transport/ws"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// SessionConfig 直连会话配置。
type SessionConfig struct {
	// BaseURL 网关地址（http/https）。
	BaseURL string
	// AgentToken Agent 鉴权令牌。
	AgentToken string
	// HeartbeatInterval 心跳（ping）间隔。
	HeartbeatInterval time.Duration
	// PongTimeout 等待 pong 的超时，超时判定连接已死。
	PongTimeout time.Duration
	// WriteTimeout 单条消息写超时。
	WriteTimeout time.Duration
	// InsecureTLS 是否跳过证书校验。
	InsecureTLS bool
	// TaskBuffer 任务通道缓冲大小。
	TaskBuffer int
}

func (c SessionConfig) withDefaults() SessionConfig {
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 20 * time.Second
	}
	if c.PongTimeout <= 0 {
		c.PongTimeout = 60 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.TaskBuffer <= 0 {
		c.TaskBuffer = 16
	}
	return c
}

// Session port.DirectSession 的 WebSocket 实现。
type Session struct {
	agentID string
	conn    *websocket.Conn
	cfg     SessionConfig
	tasks   chan port.TaskDispatch
	errs    chan error
	writeMu sync.Mutex
	closed  atomic.Bool
	once    sync.Once
	cancel  context.CancelFunc
}

// NewSession 建立到网关的直连会话（Agent 主动出站）。
func NewSession(ctx context.Context, agentID string, cfg SessionConfig) (*Session, error) {
	cfg = cfg.withDefaults()
	url := wsURL(cfg.BaseURL) + "/agent/ws?agentId=" + agentID

	header := http.Header{}
	if cfg.AgentToken != "" {
		header.Set("Authorization", "Agent-Token "+cfg.AgentToken)
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		EnableCompression: true,
		TLSClientConfig:   tlsConfig(cfg.InsecureTLS),
	}
	conn, _, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "dial gateway websocket failed", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	s := &Session{
		agentID: agentID,
		conn:    conn,
		cfg:     cfg,
		tasks:   make(chan port.TaskDispatch, cfg.TaskBuffer),
		errs:    make(chan error, 1),
		cancel:  cancel,
	}

	lastPong := &atomic.Int64{}
	lastPong.Store(time.Now().UnixNano())
	conn.SetPongHandler(func(string) error {
		lastPong.Store(time.Now().UnixNano())
		return nil
	})
	conn.SetPingHandler(func(payload string) error {
		lastPong.Store(time.Now().UnixNano())
		return s.writeRaw(websocket.PongMessage, []byte(payload))
	})

	// 握手声明身份。
	hello, _ := ws.Encode(ws.Message{Type: ws.MsgTypeHello, Message: agentID, At: time.Now()})
	if err := s.writeRaw(websocket.TextMessage, hello); err != nil {
		_ = conn.Close()
		cancel()
		return nil, err
	}

	go s.readLoop(runCtx)
	go s.heartbeat(runCtx, lastPong)
	return s, nil
}

// Tasks 任务通道。
func (s *Session) Tasks() <-chan port.TaskDispatch { return s.tasks }

// Errors 错误通道。
func (s *Session) Errors() <-chan error { return s.errs }

// Send 上报片段/结果。
func (s *Session) Send(ctx context.Context, req port.AckRequest) error {
	if s.closed.Load() {
		return apperr.New(apperr.CodeNotConnected, "session closed")
	}
	payload, err := ws.Encode(ws.Message{Type: ws.MsgTypeAck, Ack: &req, At: time.Now()})
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "encode ack failed", err)
	}
	return s.write(ctx, websocket.TextMessage, payload)
}

// Close 关闭会话。
func (s *Session) Close() error {
	var err error
	s.once.Do(func() {
		s.closed.Store(true)
		if s.cancel != nil {
			s.cancel()
		}
		s.writeMu.Lock()
		_ = s.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
		s.writeMu.Unlock()
		err = s.conn.Close()
	})
	return err
}

func (s *Session) readLoop(ctx context.Context) {
	defer func() {
		s.closed.Store(true)
		close(s.errs)
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case s.errs <- apperr.Wrap(apperr.CodeUnavailable, "websocket read failed", err):
			default:
			}
			return
		}
		msg, derr := ws.Decode(data)
		if derr != nil {
			continue
		}
		switch msg.Type {
		case ws.MsgTypeTask:
			if msg.Task == nil {
				continue
			}
			select {
			case s.tasks <- *msg.Task:
			case <-ctx.Done():
				return
			}
		case ws.MsgTypeError:
			select {
			case s.errs <- apperr.New(apperr.CodeUnavailable, msg.Message):
			default:
			}
		}
	}
}

func (s *Session) heartbeat(ctx context.Context, lastPong *atomic.Int64) {
	ticker := time.NewTicker(s.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, lastPong.Load())) > s.cfg.PongTimeout {
				select {
				case s.errs <- apperr.New(apperr.CodeTimeout, "heartbeat timeout, connection may be dead"):
				default:
				}
				_ = s.Close()
				return
			}
			if err := s.write(ctx, websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Session) write(ctx context.Context, msgType int, payload []byte) error {
	if deadline, ok := ctx.Deadline(); ok {
		s.writeMu.Lock()
		_ = s.conn.SetWriteDeadline(deadline)
		s.writeMu.Unlock()
	}
	return s.writeRaw(msgType, payload)
}

func (s *Session) writeRaw(msgType int, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	return s.conn.WriteMessage(msgType, payload)
}

// Factory port.DirectSessionFactory 实现。
type Factory struct {
	cfg SessionConfig
	log port.Logger
}

// NewDirectSessionFactory 构造直连会话工厂。
func NewDirectSessionFactory(cfg SessionConfig, log port.Logger) *Factory {
	return &Factory{cfg: cfg.withDefaults(), log: log.With(port.F("cmp", "ws_client"))}
}

// Dial 建立会话。
func (f *Factory) Dial(ctx context.Context, agentID string) (port.DirectSession, error) {
	s, err := NewSession(ctx, agentID, f.cfg)
	if err != nil {
		return nil, err
	}
	f.log.Info("direct session connected", port.F("agent", agentID))
	return s, nil
}

func wsURL(base string) string {
	base = trimSlash(base)
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	case strings.HasPrefix(base, "wss://"), strings.HasPrefix(base, "ws://"):
		return base
	}
	return "wss://" + base
}
