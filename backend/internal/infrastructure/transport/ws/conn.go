package ws

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/application/port"
)

// Conn 单条 Agent 长连接。
type Conn struct {
	agentID   string
	ws        *websocket.Conn
	hub       *Hub
	onAck     AckHandler
	log       port.Logger
	writeMu   sync.Mutex
	closed    atomic.Bool
	closeOnce sync.Once
}

func newConn(agentID string, ws *websocket.Conn, hub *Hub, onAck AckHandler, log port.Logger) *Conn {
	return &Conn{
		agentID: agentID,
		ws:      ws,
		hub:     hub,
		onAck:   onAck,
		log:     log.With(port.F("agent", agentID)),
	}
}

// AgentID 连接归属 Agent。
func (c *Conn) AgentID() string { return c.agentID }

// Closed 连接是否已关闭。
func (c *Conn) Closed() bool { return c.closed.Load() }

// Write 线程安全地写入一条消息。
func (c *Conn) Write(ctx context.Context, msgType int, payload []byte) error {
	if c.Closed() {
		return context.Canceled
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.ws.SetWriteDeadline(deadline)
	} else {
		_ = c.ws.SetWriteDeadline(time.Now().Add(c.hub.cfg.WriteTimeout))
	}
	return c.ws.WriteMessage(msgType, payload)
}

// Close 关闭连接。
func (c *Conn) Close() error { return c.closeWith(websocket.CloseNormalClosure, "bye") }

func (c *Conn) close() error { return c.closeWith(websocket.CloseNormalClosure, "replaced") }

func (c *Conn) closeWith(code int, reason string) error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.writeMu.Lock()
		_ = c.ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = c.ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason))
		c.writeMu.Unlock()
		err = c.ws.Close()
	})
	return err
}

// Serve 读循环：处理 Agent 上报与心跳，直到连接断开或 ctx 取消。
func (c *Conn) Serve(ctx context.Context) {
	defer func() {
		c.hub.unregister(c.agentID, c)
		_ = c.closeWith(websocket.CloseNormalClosure, "serve done")
	}()

	c.ws.SetReadLimit(c.hub.cfg.MaxMessageSize)
	_ = c.ws.SetReadDeadline(time.Now().Add(c.hub.cfg.ReadTimeout))
	c.ws.SetPongHandler(func(string) error {
		_ = c.ws.SetReadDeadline(time.Now().Add(c.hub.cfg.ReadTimeout))
		return nil
	})

	pingTicker := time.NewTicker(pingInterval(c.hub.cfg))
	defer pingTicker.Stop()

	// 服务端主动 ping，用于发现半开连接。
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				if c.Closed() {
					return
				}
				if err := c.Write(ctx, websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// 优雅退出：ctx 取消时关闭底层连接，唤醒读循环。
	go func() {
		<-ctx.Done()
		_ = c.closeWith(websocket.CloseNormalClosure, "server shutdown")
	}()

	for {
		if c.Closed() {
			return
		}
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.log.Debug("websocket read stopped", port.F("err", err.Error()))
			}
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(c.hub.cfg.ReadTimeout))

		msg, derr := Decode(data)
		if derr != nil {
			c.log.Warn("decode message failed", port.F("err", derr.Error()))
			continue
		}
		switch msg.Type {
		case MsgTypeAck:
			if msg.Ack == nil || c.onAck == nil {
				continue
			}
			ack := *msg.Ack
			if ack.AgentID == "" {
				ack.AgentID = c.agentID
			}
			if err := c.onAck(ctx, ack); err != nil {
				c.log.Warn("handle ack failed", port.F("err", err.Error()))
			}
		case MsgTypePing:
			payload, _ := Encode(Message{Type: MsgTypePong, At: time.Now()})
			if err := c.Write(ctx, websocket.TextMessage, payload); err != nil {
				return
			}
		case MsgTypePong, MsgTypeHello:
			// 保活与握手声明，无需额外处理。
		default:
			c.log.Debug("unknown message type", port.F("type", msg.Type))
		}
	}
}

func pingInterval(cfg HubConfig) time.Duration {
	if cfg.PingInterval > 0 {
		return cfg.PingInterval
	}
	return 30 * time.Second
}
