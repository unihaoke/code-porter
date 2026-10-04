// Package e2e 在进程内串联网关各用例，验证任务状态机与双通路的核心行为。
package e2e

import (
	"context"
	"io"
	"testing"
	"time"

	gatewayapp "github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/internal/infrastructure/broker"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"github.com/codeporter/code-porter/internal/infrastructure/persistence/memory"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// bundle 一组装配好的网关用例。
type bundle struct {
	registry  *gatewayapp.AgentRegistry
	submit    *gatewayapp.SubmitTaskUseCase
	pull      *gatewayapp.PullTasksUseCase
	ack       *gatewayapp.AckTaskUseCase
	taskRepo  *memory.TaskRepository
	queueRepo *memory.TaskQueueRepository
	broker    *broker.MemoryBroker
}

func newBundle(t *testing.T, policy gatewayapp.TaskPolicy) *bundle {
	t.Helper()
	log := logging.New(io.Discard, logging.LevelError)
	clock := port.RealClock{}
	taskRepo := memory.NewTaskRepository()
	agentRepo := memory.NewAgentRepository()
	queueRepo := memory.NewTaskQueueRepository()
	eventBroker := broker.NewMemoryBroker()

	registry := gatewayapp.NewAgentRegistry(agentRepo, queueRepo, clock, log, policy)
	// TODO(T15): 端到端用例将整体改为「建用户→登录→秘钥接入」的多租户链路。
	if _, err := registry.AuthenticateAndRegister(context.Background(), user.SeedAdminID, "local-pc", "My PC"); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	ack := gatewayapp.NewAckTaskUseCase(taskRepo, queueRepo, registry, eventBroker, clock, log, policy)
	pull := gatewayapp.NewPullTasksUseCase(taskRepo, queueRepo, registry, clock, log, policy)
	submit := gatewayapp.NewSubmitTaskUseCase(taskRepo, queueRepo, registry, eventBroker, nil, clock, log, policy)
	return &bundle{registry: registry, submit: submit, pull: pull, ack: ack,
		taskRepo: taskRepo, queueRepo: queueRepo, broker: eventBroker}
}

func basePolicy() gatewayapp.TaskPolicy {
	return gatewayapp.TaskPolicy{
		MaxRetry:       1,
		LockTimeout:    60 * time.Second,
		TTL:            10 * time.Minute,
		QueueMaxLen:    2,
		RequestTimeout: 10 * time.Second,
		MCPTimeout:     time.Minute,
	}
}

func submitCmd(mode task.DeliveryMode) gatewayapp.SubmitTaskCommand {
	return gatewayapp.SubmitTaskCommand{
		APIKeyID: "key-1",
		AgentID:  agent.ID("local-pc"),
		Model:    model.ClaudeCode,
		Messages: []task.Message{{Role: "user", Content: "帮我找出这段代码的内存泄漏"}},
		Mode:     mode,
		Stream:   false,
	}
}

// TestTenantIsolationFlow 多租户任务路由：属主只能把任务下发给自己的实例。
func TestTenantIsolationFlow(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()
	ownerB := user.ID("usr_bob")

	// B 还没有任何实例 → 503。
	_, err := b.submit.Execute(ctx, gatewayapp.SubmitTaskCommand{
		OwnerID:  ownerB,
		Model:    model.ClaudeCode,
		Messages: []task.Message{{Role: "user", Content: "hi"}},
		Mode:     task.ModePull,
	})
	if apperr.CodeOf(err) != apperr.CodeUnavailable {
		t.Fatalf("owner B no agents -> unavailable, got %v", err)
	}

	// B 自注册一台实例。
	if _, err := b.registry.AuthenticateAndRegister(ctx, ownerB, "agt_bob1", "bob-pc"); err != nil {
		t.Fatalf("register bob agent: %v", err)
	}
	// B 指定 A 的实例（种子 admin 的 local-pc）→ 404。
	_, err = b.submit.Execute(ctx, gatewayapp.SubmitTaskCommand{
		OwnerID:  ownerB,
		AgentID:  "local-pc",
		Model:    model.ClaudeCode,
		Messages: []task.Message{{Role: "user", Content: "hi"}},
		Mode:     task.ModePull,
	})
	if apperr.CodeOf(err) != apperr.CodeNotFound {
		t.Fatalf("cross-tenant explicit -> not_found, got %v", err)
	}
	// B 不指定实例（仅 1 台）→ 自动路由到自己的实例并入队。
	res, err := b.submit.Execute(ctx, gatewayapp.SubmitTaskCommand{
		OwnerID:  ownerB,
		Model:    model.ClaudeCode,
		Messages: []task.Message{{Role: "user", Content: "hi"}},
		Mode:     task.ModePull,
	})
	if err != nil {
		t.Fatalf("owner B auto-route: %v", err)
	}
	defer res.Close()
	if n, _ := b.queueRepo.Len(ctx, "agt_bob1"); n != 1 {
		t.Fatalf("task should be queued on agt_bob1, got %d", n)
	}
	if n, _ := b.queueRepo.Len(ctx, "local-pc"); n != 0 {
		t.Fatalf("seed admin queue must stay empty, got %d", n)
	}
}

// TestPullModeHappyPath Pull 模式全链路：提交 → 入队 → 拉取 → 上报片段 → 成功完成。
func TestPullModeHappyPath(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()

	res, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer res.Close()

	if res.Mode != task.ModePull {
		t.Fatalf("expected pull mode, got %s", res.Mode)
	}
	queueLen, _ := b.queueRepo.Len(ctx, "local-pc")
	if queueLen != 1 {
		t.Fatalf("expected queue length 1, got %d", queueLen)
	}

	// 模拟 LocalAgent：拉取并执行。
	go func() {
		pr, err := b.pull.Execute(ctx, gatewayapp.PullTasksQuery{AgentID: "local-pc", MaxBatch: 1})
		if err != nil || len(pr.Tasks) != 1 {
			return
		}
		d := pr.Tasks[0]
		_, _ = b.ack.Execute(ctx, gatewayapp.AckCommand{
			TaskID: d.TaskID, AgentID: "local-pc", LockToken: d.LockToken,
			Status: port.AckProgress,
			Chunks: []port.ChunkPayload{{Seq: 1, Content: "发现一处泄漏"}},
		})
		_, _ = b.ack.Execute(ctx, gatewayapp.AckCommand{
			TaskID: d.TaskID, AgentID: "local-pc", LockToken: d.LockToken,
			Status: port.AckSuccess, Result: "发现一处泄漏：conn 未关闭",
		})
	}()

	var got string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-res.Events:
			switch ev.Type {
			case port.EventChunk:
				got += ev.Content
			case port.EventDone:
				if got != "发现一处泄漏" {
					t.Fatalf("unexpected stream content: %q", got)
				}
				// Done 事件必须携带完整结果，保证「只上报 Result」的场景不丢内容。
				if ev.Content != "发现一处泄漏：conn 未关闭" {
					t.Fatalf("unexpected done content: %q", ev.Content)
				}
				return
			case port.EventError:
				t.Fatalf("unexpected error event: %s", ev.Message)
			}
		case <-deadline:
			t.Fatal("timeout waiting for task done event")
		}
	}
}

// TestQueueFullReturns429 队列满时触发限流，返回 queue_full 错误码。
func TestQueueFullReturns429(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()

	for i := 0; i < basePolicy().QueueMaxLen; i++ {
		r, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		r.Close()
	}
	_, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
	if err == nil {
		t.Fatal("expected error when queue is full")
	}
	if apperr.CodeOf(err) != apperr.CodeQueueFull {
		t.Fatalf("expected queue_full, got %s", apperr.CodeOf(err))
	}
}

// TestDirectModeRequiresConnection 直连模式在 Agent 未建立长连接时必须快速失败。
func TestDirectModeRequiresConnection(t *testing.T) {
	b := newBundle(t, basePolicy())
	_, err := b.submit.Execute(context.Background(), submitCmd(task.ModeDirect))
	if err == nil {
		t.Fatal("expected error when no websocket connection")
	}
	if apperr.CodeOf(err) != apperr.CodeNotConnected {
		t.Fatalf("expected not_connected, got %s", apperr.CodeOf(err))
	}
}

// TestBackpressureRelease 本地背压：Agent 释放任务后，任务回到网关队列等待下次拉取。
func TestBackpressureRelease(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()

	res, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer res.Close()

	pr, err := b.pull.Execute(ctx, gatewayapp.PullTasksQuery{AgentID: "local-pc", MaxBatch: 1})
	if err != nil || len(pr.Tasks) != 1 {
		t.Fatalf("pull: %v, tasks=%d", err, len(pr.Tasks))
	}
	d := pr.Tasks[0]

	// 任务被拉走后队列应清空。
	if n, _ := b.queueRepo.Len(ctx, "local-pc"); n != 0 {
		t.Fatalf("expected empty queue, got %d", n)
	}

	if _, err := b.ack.Execute(ctx, gatewayapp.AckCommand{
		TaskID: d.TaskID, AgentID: "local-pc", LockToken: d.LockToken, Status: port.AckReleased,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 释放后任务应重新回到队列，且状态回到 pending。
	if n, _ := b.queueRepo.Len(ctx, "local-pc"); n != 1 {
		t.Fatalf("expected requeued task, queue len=%d", n)
	}
	stored, err := b.taskRepo.Find(ctx, task.ID(d.TaskID))
	if err != nil {
		t.Fatalf("find task: %v", err)
	}
	if stored.Status() != task.StatusPending {
		t.Fatalf("expected pending after release, got %s", stored.Status())
	}
	if stored.Attempts() != 0 {
		t.Fatalf("release should not consume retry attempts, got %d", stored.Attempts())
	}
}

// TestRetryThenDeadLetter 失败重试耗尽后进入死信，并向调用方推送错误事件。
func TestRetryThenDeadLetter(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()

	res, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer res.Close()

	go func() {
		// 第 1 次执行失败（仍有一次重试额度）。
		pr, err := b.pull.Execute(ctx, gatewayapp.PullTasksQuery{AgentID: "local-pc", MaxBatch: 1})
		if err != nil || len(pr.Tasks) != 1 {
			return
		}
		d := pr.Tasks[0]
		if _, err := b.ack.Execute(ctx, gatewayapp.AckCommand{
			TaskID: d.TaskID, AgentID: "local-pc", LockToken: d.LockToken,
			Status: port.AckFailed, Error: "MCP server not started",
		}); err != nil {
			return
		}
		// 第 2 次执行失败（重试耗尽）。
		pr2, err := b.pull.Execute(ctx, gatewayapp.PullTasksQuery{AgentID: "local-pc", MaxBatch: 1})
		if err != nil || len(pr2.Tasks) != 1 {
			return
		}
		d2 := pr2.Tasks[0]
		_, _ = b.ack.Execute(ctx, gatewayapp.AckCommand{
			TaskID: d2.TaskID, AgentID: "local-pc", LockToken: d2.LockToken,
			Status: port.AckFailed, Error: "MCP server not started",
		})
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-res.Events:
			if ev.Type == port.EventError {
				stored, _ := b.taskRepo.Find(ctx, task.ID(res.TaskID))
				if stored.Status() != task.StatusFailed && stored.Status() != task.StatusDeadLetter {
					t.Fatalf("expected terminal failure, got %s", stored.Status())
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for terminal error event")
		}
	}
}

// TestLockTimeoutRequeue 任务锁超时后，任务被回收回队列重新调度。
func TestLockTimeoutRequeue(t *testing.T) {
	b := newBundle(t, basePolicy())
	ctx := context.Background()

	res, err := b.submit.Execute(ctx, submitCmd(task.ModePull))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer res.Close()

	pr, err := b.pull.Execute(ctx, gatewayapp.PullTasksQuery{AgentID: "local-pc", MaxBatch: 1})
	if err != nil || len(pr.Tasks) != 1 {
		t.Fatalf("pull: %v", err)
	}
	stored, _ := b.taskRepo.Find(ctx, task.ID(pr.Tasks[0].TaskID))
	if stored.Status() != task.StatusRunning {
		t.Fatalf("expected running, got %s", stored.Status())
	}

	// 模拟 Agent 宕机：任务锁到期。
	requeued, err := stored.ExpireLock(time.Now().Add(2 * time.Minute))
	if err != nil || !requeued {
		t.Fatalf("expected lock expiry to requeue task: %v", err)
	}
}
