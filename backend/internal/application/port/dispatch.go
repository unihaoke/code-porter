package port

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/domain/task"
)

// TaskDispatch 网关下发给 Agent 的任务载荷（Pull / Direct 两种通路共用同一结构）。
type TaskDispatch struct {
	TaskID      string          `json:"task_id"`
	AgentID     string          `json:"agent_id"`
	Model       string          `json:"model"`
	Stream      bool            `json:"stream"`
	Prompt      string          `json:"prompt"`
	Messages    []task.Message  `json:"messages,omitempty"`
	Files       []task.CodeFile `json:"files,omitempty"`
	Operation   string          `json:"operation"`
	WorkDir     string          `json:"work_dir,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	// Permission 本地文件操作权限（read/write/all），Agent 侧必须据此限制 CLI 能力。
	Permission string `json:"permission,omitempty"`
	// LockToken 任务锁令牌，Agent 上报时必须带回。
	LockToken string `json:"lock_token,omitempty"`
	// Attempt 当前执行次数。
	Attempt int `json:"attempt"`
	// CreatedAt 任务创建时间。
	CreatedAt time.Time `json:"created_at"`
	// TimeoutSeconds 本地 MCP 调用超时（秒）。
	TimeoutSeconds int `json:"timeout_seconds"`
}

// AckStatus 上报状态。
type AckStatus string

const (
	// AckSuccess 执行成功。
	AckSuccess AckStatus = "success"
	// AckFailed 执行失败。
	AckFailed AckStatus = "failed"
	// AckReleased 本地背压释放：Agent 拉取到本地队列满，主动放弃消费。
	AckReleased AckStatus = "released"
	// AckProgress 仅上报流式片段，任务仍在运行。
	AckProgress AckStatus = "progress"
)

// ChunkPayload 上报的流式片段。
type ChunkPayload struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// AckRequest Agent → 网关的上报请求。
type AckRequest struct {
	TaskID    string         `json:"task_id"`
	AgentID   string         `json:"agent_id"`
	LockToken string         `json:"lock_token,omitempty"`
	Status    AckStatus      `json:"status"`
	Chunks    []ChunkPayload `json:"chunks,omitempty"`
	Error     string         `json:"error,omitempty"`
	// Result 终态时的完整结果（可选，缺省时网关用已累计片段拼接）。
	Result string `json:"result,omitempty"`
}

// DirectPusher 网关侧：通过已建立的 WebSocket 长连接向 Agent 实时推送任务（SSE 直连模式）。
type DirectPusher interface {
	// Connected 判断指定 Agent 是否存在可用长连接。
	Connected(agentID string) bool
	// Push 推送任务；无可用连接返回 apperr.CodeNotConnected。
	Push(ctx context.Context, agentID string, dispatch TaskDispatch) error
	// ConnCount 当前在线长连接数。
	ConnCount() int
}
