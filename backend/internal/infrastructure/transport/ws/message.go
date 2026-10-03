// Package ws 实现网关侧的 WebSocket 长连接池（SSE 直连模式）与 LocalAgent 侧的出站会话。
package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
)

// 消息类型。
const (
	// MsgTypeTask 网关 → Agent：实时推送任务。
	MsgTypeTask = "task"
	// MsgTypeAck Agent → 网关：上报片段/结果。
	MsgTypeAck = "ack"
	// MsgTypePing 心跳请求。
	MsgTypePing = "ping"
	// MsgTypePong 心跳响应。
	MsgTypePong = "pong"
	// MsgTypeHello Agent → 网关：建连握手，声明身份。
	MsgTypeHello = "hello"
	// MsgTypeError 网关 → Agent：错误通知（如任务被拒绝）。
	MsgTypeError = "error"
)

// Message 长连接上的统一消息信封。
type Message struct {
	Type    string             `json:"type"`
	Task    *port.TaskDispatch `json:"task,omitempty"`
	Ack     *port.AckRequest   `json:"ack,omitempty"`
	Message string             `json:"message,omitempty"`
	At      time.Time          `json:"at,omitempty"`
}

// Encode 序列化消息。
func Encode(m Message) ([]byte, error) { return json.Marshal(m) }

// Decode 反序列化消息。
func Decode(data []byte) (Message, error) {
	var m Message
	err := json.Unmarshal(data, &m)
	return m, err
}

// AckHandler 处理 Agent 通过长连接上报的 ACK。
type AckHandler func(ctx context.Context, req port.AckRequest) error
