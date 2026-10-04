package agent

import (
	"context"
	"testing"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// recordingRunner 记录收到的请求并回一个片段后正常结束。
type recordingRunner struct {
	lastReq port.MCPStreamRequest
	model   model.Model
	fail    bool
}

func (r *recordingRunner) Model() model.Model { return r.model }

func (r *recordingRunner) StreamRun(_ context.Context, req port.MCPStreamRequest) (<-chan port.MCPChunk, error) {
	r.lastReq = req
	ch := make(chan port.MCPChunk, 1)
	if r.fail {
		ch <- port.MCPChunk{Err: apperr.New(apperr.CodeMCPFailure, "boom")}
	} else {
		ch <- port.MCPChunk{Content: "ok"}
	}
	close(ch)
	return ch, nil
}

func (r *recordingRunner) HealthCheck(context.Context) error { return nil }
func (r *recordingRunner) Close() error                      { return nil }

type fakeMCPRegistry struct{ runner port.MCPRunner }

func (r *fakeMCPRegistry) Get(model.Model) (port.MCPRunner, error) { return r.runner, nil }
func (r *fakeMCPRegistry) All() []port.MCPRunner                   { return []port.MCPRunner{r.runner} }
func (r *fakeMCPRegistry) HealthCheckAll(context.Context) []port.AdapterHealth {
	return nil
}
func (r *fakeMCPRegistry) Close() error { return nil }

type recordingReporter struct {
	failure string
	success string
}

func (r *recordingReporter) ReportProgress(context.Context, string, string, []port.ChunkPayload) error {
	return nil
}
func (r *recordingReporter) ReportSuccess(_ context.Context, _, _, result string) error {
	r.success = result
	return nil
}
func (r *recordingReporter) ReportFailure(_ context.Context, _, _, errMsg string) error {
	r.failure = errMsg
	return nil
}
func (r *recordingReporter) ReportRelease(context.Context, string, string) error { return nil }

// TestExecutorPassesPermissionToRunner 验证任务权限从下发载荷一路传到 MCP 适配器；
// 非法权限 fail-closed，不允许在默认放开状态下执行。
func TestExecutorPassesPermissionToRunner(t *testing.T) {
	runner := &recordingRunner{model: model.ClaudeCode}
	rep := &recordingReporter{}
	exec := NewTaskExecutor(&fakeMCPRegistry{runner: runner}, rep, nopLogger{}, Policy{})

	t.Run("read permission forwarded", func(t *testing.T) {
		*rep = recordingReporter{}
		err := exec.Execute(context.Background(), port.TaskDispatch{
			TaskID: "t1", Model: "claude-code", Permission: "read",
		})
		if err != nil || rep.failure != "" {
			t.Fatalf("execute: err=%v failure=%s", err, rep.failure)
		}
		if runner.lastReq.Permission != task.PermissionRead {
			t.Fatalf("runner got permission %q, want read", runner.lastReq.Permission)
		}
	})

	t.Run("empty permission defaults to all", func(t *testing.T) {
		*rep = recordingReporter{}
		if err := exec.Execute(context.Background(), port.TaskDispatch{
			TaskID: "t2", Model: "claude-code",
		}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if runner.lastReq.Permission != task.PermissionAll {
			t.Fatalf("empty permission must default to all, got %q", runner.lastReq.Permission)
		}
	})

	t.Run("garbage permission rejected fail-closed", func(t *testing.T) {
		*rep = recordingReporter{}
		before := runner.lastReq.Permission
		err := exec.Execute(context.Background(), port.TaskDispatch{
			TaskID: "t3", Model: "claude-code", Permission: "sudo-rm-rf",
		})
		if err == nil || rep.failure == "" {
			t.Fatalf("invalid permission must fail the task, err=%v failure=%q", err, rep.failure)
		}
		if runner.lastReq.Permission != before {
			t.Fatalf("runner must not be invoked for invalid permission")
		}
	})
}
