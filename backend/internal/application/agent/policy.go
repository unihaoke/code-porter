// Package agent 是 LocalAgent 侧的应用服务层：任务消费、执行、上报与健康检查。
package agent

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
)

// Policy 本地 Agent 的运行策略，由 yaml 配置注入。
type Policy struct {
	// PullIntervalMin 有任务时的最快轮询间隔。
	PullIntervalMin time.Duration
	// PullIntervalMax 长时间空闲后的最大轮询间隔。
	PullIntervalMax time.Duration
	// BackoffFactor 空轮询退避系数。
	BackoffFactor float64
	// MaxConcurrency 本地协程池硬上限（保护 IDE，默认 2）。
	MaxConcurrency int
	// QueueSize 本地内部等待队列长度。
	QueueSize int
	// MCPTimeout 单次 MCP 调用超时，超时强制释放 worker。
	MCPTimeout time.Duration
	// ChunkFlushInterval 片段批量上报的最大间隔。
	ChunkFlushInterval time.Duration
	// ChunkBatchSize 片段批量上报的条数阈值。
	ChunkBatchSize int
	// HeartbeatInterval 健康上报间隔。
	HeartbeatInterval time.Duration
	// ReconnectMin / ReconnectMax 直连模式断线重连退避区间。
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func (p Policy) withDefaults() Policy {
	if p.PullIntervalMin <= 0 {
		p.PullIntervalMin = 500 * time.Millisecond
	}
	if p.PullIntervalMax < p.PullIntervalMin {
		p.PullIntervalMax = 3 * time.Second
	}
	if p.BackoffFactor <= 0 {
		p.BackoffFactor = 1.6
	}
	if p.MaxConcurrency <= 0 {
		p.MaxConcurrency = 2
	}
	if p.QueueSize <= 0 {
		p.QueueSize = 4
	}
	if p.MCPTimeout <= 0 {
		p.MCPTimeout = 3 * time.Minute
	}
	if p.ChunkFlushInterval <= 0 {
		p.ChunkFlushInterval = 300 * time.Millisecond
	}
	if p.ChunkBatchSize <= 0 {
		p.ChunkBatchSize = 8
	}
	if p.HeartbeatInterval <= 0 {
		p.HeartbeatInterval = 15 * time.Second
	}
	if p.ReconnectMin <= 0 {
		p.ReconnectMin = time.Second
	}
	if p.ReconnectMax <= p.ReconnectMin {
		p.ReconnectMax = 30 * time.Second
	}
	return p
}

// SystemProbe 本机资源探测端口（CPU / 内存 / 主机名 / 各 MCP 可用性）。
type SystemProbe interface {
	// Probe 采集一次健康快照。
	Probe(ctx context.Context, inflight, queued, maxConcurrency int, mcps []agent.MCPHealth) (agent.Health, error)
}

// MCPProber MCP 适配器可用性探测端口。
type MCPProber interface {
	// Health 返回全部适配器的可用性。
	Health(ctx context.Context) []ModelHealth
}

// ModelHealth 单个 MCP 适配器的健康探测结果。
type ModelHealth struct {
	Model     model.Model
	Available bool
	Detail    string
}
