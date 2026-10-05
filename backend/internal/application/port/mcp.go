package port

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// ChunkKind 片段的语义类型，用于把「思考/工具调用过程」与「最终正文」分开。
type ChunkKind string

const (
	// ChunkText 正文增量（对用户可见的答案文本）；零值也按正文处理，兼容旧适配器。
	ChunkText ChunkKind = "text"
	// ChunkThinking 模型思考过程增量（reasoning），不进入最终结果，仅过程展示。
	ChunkThinking ChunkKind = "thinking"
	// ChunkTool 工具调用/工具输出的过程行（如「🔧 Bash(ls -l)」），不进入最终结果。
	ChunkTool ChunkKind = "tool"
)

// MCPChunk MCP 适配器输出的流式片段。
type MCPChunk struct {
	// Seq 序号，从 1 开始单调递增。
	Seq int
	// Kind 片段类型；零值按 ChunkText 处理。
	Kind ChunkKind
	// Content 文本增量。
	Content string
	// Err 非空表示该流以错误终止，消费者应停止继续读取。
	Err error
}

// IsBody 报告该片段是否属于「最终正文」（网关结果 / SSE / 任务结论只拼正文）。
func (k ChunkKind) IsBody() bool {
	return k == "" || k == ChunkText
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
	// Permission 本地文件操作权限上限，适配器必须据此收紧 CLI 沙箱/权限模式。
	Permission task.Permission
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
