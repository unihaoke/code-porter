// Package gateway 是网关侧的应用服务层（用例编排）。
package gateway

import (
	"time"

	"github.com/codeporter/code-porter/internal/domain/task"
)

// TaskPolicy 任务相关的业务策略，由配置注入。
type TaskPolicy struct {
	// MaxRetry 最大重试次数（超过进入死信）。
	MaxRetry int
	// LockTimeout 单次执行的任务锁时长。
	LockTimeout time.Duration
	// TTL 任务整体存活时长（从创建起算）。
	TTL time.Duration
	// QueueMaxLen 单 Agent 私有队列最大长度。
	QueueMaxLen int
	// RequestTimeout 外部调用方最长等待时间（非流式）。
	RequestTimeout time.Duration
	// StreamIdleTimeout 流式连接最长空闲时间（无任何片段到达）。
	StreamIdleTimeout time.Duration
	// MCPTimeout 下发给 Agent 的本地 MCP 调用超时。
	MCPTimeout time.Duration
}

// WithDefaults 导出版本，供基础设施层在构造处理器时填充默认值。
func (p TaskPolicy) WithDefaults() TaskPolicy { return p.withDefaults() }

// withDefaults 填充默认值，避免零值导致任务瞬间过期。
func (p TaskPolicy) withDefaults() TaskPolicy {
	if p.LockTimeout <= 0 {
		p.LockTimeout = 60 * time.Second
	}
	if p.TTL <= 0 {
		p.TTL = 10 * time.Minute
	}
	if p.QueueMaxLen <= 0 {
		p.QueueMaxLen = task.DefaultQueueMaxLen
	}
	if p.RequestTimeout <= 0 {
		p.RequestTimeout = 5 * time.Minute
	}
	if p.StreamIdleTimeout <= 0 {
		p.StreamIdleTimeout = 2 * time.Minute
	}
	if p.MCPTimeout <= 0 {
		p.MCPTimeout = 3 * time.Minute
	}
	if p.MaxRetry < 0 {
		p.MaxRetry = 0
	}
	return p
}
