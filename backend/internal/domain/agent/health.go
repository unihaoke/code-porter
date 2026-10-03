package agent

import (
	"time"

	"github.com/codeporter/code-porter/internal/domain/model"
)

// MCPHealth 单个 MCP 适配器的可用性。
type MCPHealth struct {
	// Model 对应的本地 AI 工具。
	Model model.Model `json:"model"`
	// Available 是否可用（进程已启动且 MCP 握手成功）。
	Available bool `json:"available"`
	// Detail 不可用时的可读原因（如「Trae 未启动」）。
	Detail string `json:"detail,omitempty"`
}

// Health 本机健康快照（LocalAgent 定时上报）。
type Health struct {
	// CPUPercent CPU 使用率。
	CPUPercent float64 `json:"cpu_percent"`
	// MemPercent 内存使用率。
	MemPercent float64 `json:"mem_percent"`
	// Inflight 本地正在执行的任务数。
	Inflight int `json:"inflight"`
	// Queued 本地排队任务数。
	Queued int `json:"queued"`
	// MaxConcurrency 本地协程池上限。
	MaxConcurrency int `json:"max_concurrency"`
	// MCPs 各 MCP 适配器可用状态。
	MCPs []MCPHealth `json:"mcps"`
	// Hostname 主机名。
	Hostname string `json:"hostname"`
	// OS 操作系统。
	OS string `json:"os"`
	// UpdatedAt 快照时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// MCPAvailable 查询指定模型对应的 MCP 是否可用。
func (h Health) MCPAvailable(m model.Model) bool {
	for _, mcp := range h.MCPs {
		if mcp.Model == m {
			return mcp.Available
		}
	}
	return false
}

// IsOverload 本机是否过载（协程池跑满且还有排队）。
func (h Health) IsOverload() bool {
	return h.MaxConcurrency > 0 && h.Inflight >= h.MaxConcurrency && h.Queued > 0
}
