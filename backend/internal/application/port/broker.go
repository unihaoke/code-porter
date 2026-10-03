package port

import (
	"context"
	"sync"
	"time"
)

// EventType 任务事件类型。
type EventType string

const (
	// EventChunk 流式片段增量。
	EventChunk EventType = "chunk"
	// EventDone 任务成功完成。
	EventDone EventType = "done"
	// EventError 任务失败/超时/死信。
	EventError EventType = "error"
	// EventRejected 任务被拒绝（队列满、Agent 离线等），调用方应立即返回错误。
	EventRejected EventType = "rejected"
)

// IsTerminal 是否为终止事件。
func (t EventType) IsTerminal() bool {
	return t == EventDone || t == EventError || t == EventRejected
}

// TaskEvent 网关内部任务事件，用于把本地 AI 的输出实时扇出给等待中的 HTTP/SSE 连接。
type TaskEvent struct {
	TaskID  string    `json:"task_id"`
	Type    EventType `json:"type"`
	Content string    `json:"content,omitempty"`
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// Subscription 单个调用方对某个任务的订阅。
type Subscription struct {
	taskID string
	ch     chan TaskEvent
	once   sync.Once
	done   chan struct{}
}

// NewSubscription 构造订阅，由 TaskEventBroker 的实现调用。
func NewSubscription(taskID string, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 32
	}
	return &Subscription{taskID: taskID, ch: make(chan TaskEvent, buffer), done: make(chan struct{})}
}

// TaskID 订阅的任务 ID。
func (s *Subscription) TaskID() string { return s.taskID }

// Events 只读事件通道。
func (s *Subscription) Events() <-chan TaskEvent { return s.ch }

// Done 订阅关闭信号。
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Deliver 由 TaskEventBroker 实现调用，非阻塞投递（订阅者未及时消费则丢弃最旧事件，
// 保证调用方总能读到最新进展与终止事件，避免慢消费者拖垮整条任务链路）。
func (s *Subscription) Deliver(ev TaskEvent) {
	select {
	case s.ch <- ev:
	default:
		// 慢消费者保护：丢弃最旧的一条再写入，保证调用方总能读到最新进展与终止事件。
		select {
		case <-s.ch:
		default:
		}
		select {
		case s.ch <- ev:
		default:
		}
	}
}

// Close 关闭订阅，重复调用安全。
func (s *Subscription) Close() {
	s.once.Do(func() { close(s.done) })
}

// TaskEventBroker 任务事件总线端口。
type TaskEventBroker interface {
	// Subscribe 订阅任务事件；必须在任务入队/推送之前完成，避免竞态丢事件。
	Subscribe(ctx context.Context, taskID string, buffer int) (*Subscription, error)
	// Publish 发布事件；终止事件发布后订阅会被自动回收。
	Publish(ctx context.Context, ev TaskEvent) error
	// Unsubscribe 显式回收订阅。
	Unsubscribe(taskID string)
	// SubscriberCount 当前订阅数，用于可观测性。
	SubscriberCount() int
}
