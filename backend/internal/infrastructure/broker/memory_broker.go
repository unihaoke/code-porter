// Package broker 提供 port.TaskEventBroker 的内存实现：把本地 AI 的输出实时扇出给等待中的调用方。
package broker

import (
	"context"
	"sync"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// MemoryBroker 进程内事件总线。
//
// 订阅在任务入队之前建立，终止事件发布后自动回收订阅，避免泄漏。
type MemoryBroker struct {
	mu   sync.RWMutex
	subs map[string]*port.Subscription
}

// NewMemoryBroker 构造事件总线。
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{subs: make(map[string]*port.Subscription)}
}

// Subscribe 订阅任务事件。
func (b *MemoryBroker) Subscribe(_ context.Context, taskID string, buffer int) (*port.Subscription, error) {
	if taskID == "" {
		return nil, apperr.New(apperr.CodeInvalidParam, "task id is required")
	}
	sub := port.NewSubscription(taskID, buffer)
	b.mu.Lock()
	b.subs[taskID] = sub
	b.mu.Unlock()
	return sub, nil
}

// Publish 发布事件；终止事件发布后自动回收订阅。
func (b *MemoryBroker) Publish(_ context.Context, ev port.TaskEvent) error {
	if ev.TaskID == "" {
		return apperr.New(apperr.CodeInvalidParam, "task id is required")
	}
	b.mu.RLock()
	sub, ok := b.subs[ev.TaskID]
	b.mu.RUnlock()
	if !ok {
		// 无订阅者（如非流式调用已断开）：静默丢弃，不算错误。
		return nil
	}
	sub.Deliver(ev)
	if ev.Type.IsTerminal() {
		b.Unsubscribe(ev.TaskID)
		sub.Close()
	}
	return nil
}

// Unsubscribe 显式回收订阅。
func (b *MemoryBroker) Unsubscribe(taskID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, taskID)
}

// SubscriberCount 当前订阅数。
func (b *MemoryBroker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
