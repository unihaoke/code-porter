package port

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// MCPChunk MCP 适配器输出的流式片段。
type MCPChunk struct {
	// Seq 序号，从 1 开始单调递增。
	Seq int
	// Content 文本增量。
	Content string
	// Err 非空表示该流以错误终止，消费者应停止继续读取。
	Err error
}

// MCPStreamRequest 交给 MCP 适配器的标准化请求。
type MCPStreamRequest struct {
	TaskID      string
	Model       model.Model
	Prompt      string
	Messages    []task.Message
	Files       []task.CodeFile
	Operation   task.Operation
	WorkDir     string
	Temperature *float64
	MaxTokens   int
}

// MCPRunner 单个本地 AI 工具的 MCP 适配器（PRD 5.3 核心扩展点）。
type MCPRunner interface {
	// Model 该适配器对应的本地 AI 工具。
	Model() model.Model
	// StreamRun 启动一次流式调用，返回只读片段通道。
	// 通道在流结束或 ctx 取消后必须被关闭；调用超时由调用方通过 ctx 控制。
	StreamRun(ctx context.Context, req MCPStreamRequest) (<-chan MCPChunk, error)
	// HealthCheck 探测本地 AI 软件 / MCP Server 是否就绪。
	HealthCheck(ctx context.Context) error
	// Close 释放适配器持有的子进程与连接。
	Close() error
}

// MCPRegistry 适配器注册表：按 model 路由到具体实现。
type MCPRegistry interface {
	// Get 获取适配器，未注册返回 apperr.CodeNotFound。
	Get(m model.Model) (MCPRunner, error)
	// All 返回全部已注册适配器。
	All() []MCPRunner
	// HealthCheckAll 并发探测全部适配器可用性。
	HealthCheckAll(ctx context.Context) []AdapterHealth
	// Close 释放全部适配器资源。
	Close() error
}

// AdapterHealth 单个适配器的健康探测结果。
type AdapterHealth struct {
	Model     model.Model
	Available bool
	Detail    string
}
