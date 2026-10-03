package port

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/agent"
)

// GatewayClient LocalAgent → Gateway 的出站客户端端口（Pull 模式 + 控制面）。
//
// 所有连接均由本地主动发起，本机不监听任何端口（PRD 安全要求）。
type GatewayClient interface {
	// Pull 拉取待处理任务；maxBatch 由本地协程池剩余容量决定。
	Pull(ctx context.Context, agentID string, maxBatch int) ([]TaskDispatch, error)
	// Ack 上报任务结果与流式片段。
	Ack(ctx context.Context, req AckRequest) error
	// ReportHealth 上报本机健康状态。
	ReportHealth(ctx context.Context, agentID string, h agent.Health) error
}

// DirectSession LocalAgent → Gateway 的 WebSocket 直连会话（SSE 直连模式）。
type DirectSession interface {
	// Tasks 只读任务通道，网关实时推送的任务从这里流出。
	Tasks() <-chan TaskDispatch
	// Send 上报片段/结果，走同一条长连接，避免额外 HTTP 往返。
	Send(ctx context.Context, req AckRequest) error
	// Errors 连接层错误通道（读空表示会话结束）。
	Errors() <-chan error
	// Close 关闭会话。
	Close() error
}

// DirectSessionFactory 建立/重连直连会话，由基础设施层实现。
type DirectSessionFactory interface {
	// Dial 建立会话；内部实现需包含心跳保活与断线重连。
	Dial(ctx context.Context, agentID string) (DirectSession, error)
}
