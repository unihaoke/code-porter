package agent

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// bodyChunks 只保留正文片段：思考过程/工具调用过程属于本机执行细节，
// 既不进入网关任务结果，也不应作为正文扇出给 SSE 调用方（同时减少无谓出站流量）。
func bodyChunks(chunks []port.ChunkPayload) []port.ChunkPayload {
	out := chunks[:0:0]
	for _, c := range chunks {
		if c.Kind.IsBody() {
			out = append(out, c)
		}
	}
	return out
}

// ResultReporter 结果上报端口：屏蔽 Pull（HTTP ACK）与 Direct（WebSocket 回推）的差异。
type ResultReporter interface {
	// ReportProgress 批量上报流式片段，任务仍在执行。
	ReportProgress(ctx context.Context, taskID, lockToken string, chunks []port.ChunkPayload) error
	// ReportSuccess 上报成功并附带完整结果。
	ReportSuccess(ctx context.Context, taskID, lockToken, result string) error
	// ReportFailure 上报失败，由网关裁决重试或死信。
	ReportFailure(ctx context.Context, taskID, lockToken, errMsg string) error
	// ReportRelease 本地协程池已满，主动放弃任务（实现背压）。
	ReportRelease(ctx context.Context, taskID, lockToken string) error
}

// HTTPReporter 基于 ACK 接口的上报实现（Pull 模式）。
type HTTPReporter struct {
	client  port.GatewayClient
	agentID string
}

// NewHTTPReporter 构造 HTTP 上报器。
func NewHTTPReporter(client port.GatewayClient, agentID string) *HTTPReporter {
	return &HTTPReporter{client: client, agentID: agentID}
}

// ReportProgress 上报片段（仅正文；思考/工具过程不出本机）。
func (r *HTTPReporter) ReportProgress(ctx context.Context, taskID, lockToken string, chunks []port.ChunkPayload) error {
	chunks = bodyChunks(chunks)
	if len(chunks) == 0 {
		return nil
	}
	return r.client.Ack(ctx, port.AckRequest{
		TaskID:    taskID,
		AgentID:   r.agentID,
		LockToken: lockToken,
		Status:    port.AckProgress,
		Chunks:    chunks,
	})
}

// ReportSuccess 上报成功。
func (r *HTTPReporter) ReportSuccess(ctx context.Context, taskID, lockToken, result string) error {
	return r.client.Ack(ctx, port.AckRequest{
		TaskID:    taskID,
		AgentID:   r.agentID,
		LockToken: lockToken,
		Status:    port.AckSuccess,
		Result:    result,
	})
}

// ReportFailure 上报失败。
func (r *HTTPReporter) ReportFailure(ctx context.Context, taskID, lockToken, errMsg string) error {
	return r.client.Ack(ctx, port.AckRequest{
		TaskID:    taskID,
		AgentID:   r.agentID,
		LockToken: lockToken,
		Status:    port.AckFailed,
		Error:     errMsg,
	})
}

// ReportRelease 上报背压释放。
func (r *HTTPReporter) ReportRelease(ctx context.Context, taskID, lockToken string) error {
	return r.client.Ack(ctx, port.AckRequest{
		TaskID:    taskID,
		AgentID:   r.agentID,
		LockToken: lockToken,
		Status:    port.AckReleased,
	})
}

// DirectReporter 基于 WebSocket 长连接的上报实现（SSE 直连模式）。
type DirectReporter struct {
	session port.DirectSession
	agentID string
}

// NewDirectReporter 构造直连上报器。
func NewDirectReporter(session port.DirectSession, agentID string) *DirectReporter {
	return &DirectReporter{session: session, agentID: agentID}
}

// ReportProgress 上报片段（仅正文；思考/工具过程不出本机）。
func (r *DirectReporter) ReportProgress(ctx context.Context, taskID, lockToken string, chunks []port.ChunkPayload) error {
	chunks = bodyChunks(chunks)
	if len(chunks) == 0 {
		return nil
	}
	return r.session.Send(ctx, port.AckRequest{
		TaskID: taskID, AgentID: r.agentID, LockToken: lockToken,
		Status: port.AckProgress, Chunks: chunks,
	})
}

// ReportSuccess 上报成功。
func (r *DirectReporter) ReportSuccess(ctx context.Context, taskID, lockToken, result string) error {
	if r.session == nil {
		return apperr.New(apperr.CodeNotConnected, "direct session closed")
	}
	return r.session.Send(ctx, port.AckRequest{
		TaskID: taskID, AgentID: r.agentID, LockToken: lockToken,
		Status: port.AckSuccess, Result: result,
	})
}

// ReportFailure 上报失败。
func (r *DirectReporter) ReportFailure(ctx context.Context, taskID, lockToken, errMsg string) error {
	if r.session == nil {
		return apperr.New(apperr.CodeNotConnected, "direct session closed")
	}
	return r.session.Send(ctx, port.AckRequest{
		TaskID: taskID, AgentID: r.agentID, LockToken: lockToken,
		Status: port.AckFailed, Error: errMsg,
	})
}

// ReportRelease 上报背压释放。
func (r *DirectReporter) ReportRelease(ctx context.Context, taskID, lockToken string) error {
	if r.session == nil {
		return apperr.New(apperr.CodeNotConnected, "direct session closed")
	}
	return r.session.Send(ctx, port.AckRequest{
		TaskID: taskID, AgentID: r.agentID, LockToken: lockToken,
		Status: port.AckReleased,
	})
}
