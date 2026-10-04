package ws

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// HubConfig 长连接池配置。
type HubConfig struct {
	// WriteTimeout 单条消息写超时。
	WriteTimeout time.Duration
	// ReadTimeout 读超时（配合 ping/pong 判定死连接）。
	ReadTimeout time.Duration
	// PingInterval 服务端主动 ping 间隔；0 表示不主动 ping。
	PingInterval time.Duration
	// MaxMessageSize 单条消息最大字节数。
	MaxMessageSize int64
}

func (c HubConfig) withDefaults() HubConfig {
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 120 * time.Second
	}
	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = 8 << 20 // 8MB
	}
	return c
}

// Hub 维护 Agent 长连接，实现 port.DirectPusher。
//
// 单 Agent 只保留一条可用连接（新连接覆盖旧连接），
// 便于 direct 模式快速定位推送目标。
type Hub struct {
	mu       sync.RWMutex
	conns    map[string]*Conn
	cfg      HubConfig
	upgrader websocket.Upgrader
	onAck    AckHandler
	log      port.Logger
}

// NewHub 构造连接池。
func NewHub(cfg HubConfig, onAck AckHandler, log port.Logger) *Hub {
	cfg = cfg.withDefaults()
	return &Hub{
		conns: make(map[string]*Conn),
		cfg:   cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    4096,
			WriteBufferSize:   4096,
			HandshakeTimeout:  10 * time.Second,
			CheckOrigin:       func(r *http.Request) bool { return true },
			EnableCompression: true,
		},
		onAck: onAck,
		log:   log.With(port.F("cmp", "ws_hub")),
	}
}

// Connected 指定 Agent 是否存在可用长连接。
func (h *Hub) Connected(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.conns[agentID]
	return ok && !c.Closed()
}

// ConnCount 当前在线连接数。
func (h *Hub) ConnCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// Disconnect 主动关闭指定实例的连接（用户删除/秘钥吊销时回收在线连接）。
// 关闭动作幂等：连接断开时读循环会自行 unregister。
func (h *Hub) Disconnect(agentID string) {
	h.mu.RLock()
	c, ok := h.conns[agentID]
	h.mu.RUnlock()
	if ok {
		c.Close()
	}
}

// Push 通过长连接推送任务。
func (h *Hub) Push(ctx context.Context, agentID string, dispatch port.TaskDispatch) error {
	h.mu.RLock()
	c, ok := h.conns[agentID]
	h.mu.RUnlock()
	if !ok || c.Closed() {
		return apperr.New(apperr.CodeNotConnected, "agent "+agentID+" has no alive websocket connection")
	}
	payload, err := Encode(Message{Type: MsgTypeTask, Task: &dispatch, At: time.Now()})
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "encode task failed", err)
	}
	if err := c.Write(ctx, websocket.TextMessage, payload); err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "push task to agent failed", err)
	}
	return nil
}

// BroadcastError 向指定 Agent 推送错误通知（可选，用于运维诊断）。
func (h *Hub) BroadcastError(ctx context.Context, agentID, msg string) error {
	h.mu.RLock()
	c, ok := h.conns[agentID]
	h.mu.RUnlock()
	if !ok {
		return apperr.New(apperr.CodeNotConnected, "agent not connected")
	}
	payload, err := Encode(Message{Type: MsgTypeError, Message: msg, At: time.Now()})
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.TextMessage, payload)
}

// register 注册连接（由 HTTP 升级入口调用）。
func (h *Hub) register(agentID string, c *Conn) {
	h.mu.Lock()
	if old, ok := h.conns[agentID]; ok && old != c {
		_ = old.close()
	}
	h.conns[agentID] = c
	h.mu.Unlock()
	h.log.Info("agent websocket registered", port.F("agent", agentID))
}

// Upgrade 完成 WebSocket 握手并接管读循环（由 HTTP 层在鉴权通过后调用）。
func (h *Hub) Upgrade(ctx context.Context, w http.ResponseWriter, r *http.Request, agentID string) error {
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "websocket upgrade failed", err)
	}
	c := newConn(agentID, wsConn, h, h.onAck, h.log)
	h.register(agentID, c)
	go c.Serve(ctx)
	return nil
}

func (h *Hub) unregister(agentID string, c *Conn) {
	h.mu.Lock()
	if cur, ok := h.conns[agentID]; ok && cur == c {
		delete(h.conns, agentID)
	}
	h.mu.Unlock()
	h.log.Info("agent websocket unregistered", port.F("agent", agentID))
}
