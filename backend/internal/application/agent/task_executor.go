package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// TaskExecutor 单个任务的执行器：MCP 调用 + 流式片段上报 + 超时熔断 + panic 兜底。
//
// 每个任务在协程池的一个 worker 中运行，任何异常都不会影响池本身。
type TaskExecutor struct {
	registry port.MCPRegistry
	reporter ResultReporter
	log      port.Logger
	policy   Policy
	// OnEvent 任务执行事件回调（可选），供 GUI / IPC 层做进度展示。
	// 纯旁路：回调 panic 或阻塞都不应影响任务执行，故调用点均做保护。
	OnEvent func(TaskEvent)
}

// TaskEvent 任务执行阶段事件。
type TaskEvent struct {
	TaskID  string        `json:"task_id"`
	Model   string        `json:"model"`
	Phase   string        `json:"phase"` // start | success | failed
	Elapsed time.Duration `json:"-"`
	Detail  string        `json:"detail,omitempty"`
}

// 任务事件阶段常量。
const (
	TaskPhaseStart   = "start"
	TaskPhaseSuccess = "success"
	TaskPhaseFailed  = "failed"
)

// emit 触发事件回调，隔离回调自身可能带来的问题。
func (e *TaskExecutor) emit(ev TaskEvent) {
	if e.OnEvent == nil {
		return
	}
	defer func() { _ = recover() }()
	go e.OnEvent(ev)
}

// NewTaskExecutor 构造执行器。
func NewTaskExecutor(registry port.MCPRegistry, reporter ResultReporter, log port.Logger, policy Policy) *TaskExecutor {
	return &TaskExecutor{
		registry: registry,
		reporter: reporter,
		log:      log.With(port.F("svc", "task_executor")),
		policy:   policy.withDefaults(),
	}
}

// Execute 使用默认上报器执行任务。
func (e *TaskExecutor) Execute(ctx context.Context, d port.TaskDispatch) error {
	return e.ExecuteWith(ctx, d, nil)
}

// ExecuteWith 执行一个任务；reporter 为 nil 时使用构造时注入的默认上报器
// （直连模式需要按会话动态注入上报器）。
//
// 返回的 error 仅用于日志，结果已通过 reporter 上报网关。
func (e *TaskExecutor) ExecuteWith(ctx context.Context, d port.TaskDispatch, reporter ResultReporter) error {
	if reporter == nil {
		reporter = e.reporter
	}
	log := e.log.With(port.F("task_id", d.TaskID), port.F("model", d.Model))
	start := time.Now()

	// 单任务兜底：无论发生什么都不允许把 worker 打崩。
	var err error

	// 结束事件 defer 必须先于下面的 panic 恢复 defer 注册：
	// defer 是 LIFO，先注册者后执行，这样它才能读到 panic 恢复里赋的 err，
	// 否则任务 panic 时会被误报成 success。
	defer func() {
		phase := TaskPhaseSuccess
		detail := ""
		if err != nil {
			phase = TaskPhaseFailed
			detail = err.Error()
		}
		e.emit(TaskEvent{
			TaskID: d.TaskID, Model: string(d.Model),
			Phase: phase, Elapsed: time.Since(start), Detail: detail,
		})
	}()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			log.Error("task panicked", port.F("panic", r))
			_ = reporter.ReportFailure(context.Background(), d.TaskID, d.LockToken, err.Error())
		}
	}()

	timeout := e.policy.MCPTimeout
	if d.TimeoutSeconds > 0 {
		timeout = time.Duration(d.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.emit(TaskEvent{
		TaskID: d.TaskID, Model: string(d.Model), Phase: TaskPhaseStart,
	})

	m, perr := model.Parse(d.Model)
	if perr != nil {
		err = perr
		_ = reporter.ReportFailure(ctx, d.TaskID, d.LockToken, perr.Error())
		return err
	}

	runner, rerr := e.registry.Get(m)
	if rerr != nil {
		err = rerr
		_ = reporter.ReportFailure(ctx, d.TaskID, d.LockToken, rerr.Error())
		return err
	}

	stream, serr := runner.StreamRun(runCtx, port.MCPStreamRequest{
		TaskID:      d.TaskID,
		Model:       m,
		Prompt:      d.Prompt,
		Messages:    d.Messages,
		Files:       d.Files,
		Operation:   task.ParseOperation(d.Operation),
		WorkDir:     d.WorkDir,
		Temperature: d.Temperature,
		MaxTokens:   d.MaxTokens,
	})
	if serr != nil {
		err = serr
		msg := "mcp call failed: " + apperr.MessageOf(serr)
		log.Error(msg, port.F("err", serr.Error()))
		_ = reporter.ReportFailure(ctx, d.TaskID, d.LockToken, msg)
		return err
	}

	var sb strings.Builder
	seq := 0
	pending := make([]port.ChunkPayload, 0, e.policy.ChunkBatchSize)
	lastFlush := time.Now()

	flush := func(final bool) {
		if len(pending) == 0 {
			return
		}
		batch := pending
		pending = pending[:0]
		if rerr := reporter.ReportProgress(runCtx, d.TaskID, d.LockToken, batch); rerr != nil {
			log.Warn("report chunks failed", port.F("err", rerr.Error()), port.F("count", len(batch)))
		}
		lastFlush = time.Now()
		_ = final
	}

	for chunk := range stream {
		if chunk.Err != nil {
			err = chunk.Err
			msg := "mcp stream error: " + chunk.Err.Error()
			log.Error(msg)
			_ = reporter.ReportFailure(ctx, d.TaskID, d.LockToken, msg)
			return err
		}
		if chunk.Content == "" {
			continue
		}
		seq++
		sb.WriteString(chunk.Content)
		pending = append(pending, port.ChunkPayload{Seq: seq, Content: chunk.Content})
		if len(pending) >= e.policy.ChunkBatchSize || time.Since(lastFlush) >= e.policy.ChunkFlushInterval {
			flush(false)
		}
	}
	flush(true)

	if runCtx.Err() != nil && !errors.Is(runCtx.Err(), context.Canceled) {
		// 超时熔断：强制结束并释放 worker（PRD 5.2-6）。
		err = apperr.Wrap(apperr.CodeTimeout, "mcp call timeout", runCtx.Err())
		_ = reporter.ReportFailure(ctx, d.TaskID, d.LockToken, err.Error())
		log.Warn("task timeout", port.F("timeout", timeout.String()))
		return err
	}

	result := sb.String()
	if err := reporter.ReportSuccess(ctx, d.TaskID, d.LockToken, result); err != nil {
		log.Error("report success failed", port.F("err", err.Error()))
		return err
	}
	log.Info("task finished",
		port.F("chars", len(result)),
		port.F("chunks", seq),
		port.F("cost_ms", time.Since(start).Milliseconds()))
	return nil
}
