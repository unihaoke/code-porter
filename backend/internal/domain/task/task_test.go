package task

import (
	"testing"
	"time"
)

func newSpec() Spec {
	return Spec{
		AgentID:     "local-pc",
		Model:       "claude-code",
		Mode:        ModePull,
		MaxRetry:    1,
		LockTimeout: 30 * time.Second,
		TTL:         5 * time.Minute,
		Now:         time.Unix(1700000000, 0),
		Request:     Request{Prompt: "find memory leak", Operation: OperationDebug},
	}
}

func TestNewTaskValidation(t *testing.T) {
	if _, err := NewTask(Spec{}); err == nil {
		t.Fatal("expected error for empty spec")
	}
	spec := newSpec()
	spec.Request = Request{}
	if _, err := NewTask(spec); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestTaskLifecycle(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tk, err := NewTask(newSpec())
	if err != nil {
		t.Fatalf("new task: %v", err)
	}
	if tk.Status() != StatusPending {
		t.Fatalf("expected pending, got %s", tk.Status())
	}
	if err := tk.MarkRunning("lock-1", 30*time.Second, now); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if tk.Attempts() != 1 || tk.Status() != StatusRunning {
		t.Fatalf("unexpected state: %s/%d", tk.Status(), tk.Attempts())
	}
	// 重复加锁应被拒绝。
	if err := tk.MarkRunning("lock-2", 30*time.Second, now); err == nil {
		t.Fatal("expected error on double lock")
	}
	// 追加片段。
	if _, err := tk.AppendChunk("part-1", now); err != nil {
		t.Fatalf("append chunk: %v", err)
	}
	if _, err := tk.AppendChunk("part-2", now); err != nil {
		t.Fatalf("append chunk: %v", err)
	}
	if got := tk.Result(); got != "part-1part-2" {
		t.Fatalf("unexpected result: %q", got)
	}
	if err := tk.Complete("", now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if tk.Status() != StatusSuccess || !tk.Status().IsTerminal() {
		t.Fatalf("expected success terminal, got %s", tk.Status())
	}
	// 终态不允许再追加片段。
	if _, err := tk.AppendChunk("x", now); err == nil {
		t.Fatal("expected error appending chunk to finished task")
	}
}

func TestTaskFailThenRetryThenDeadLetter(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tk, _ := NewTask(newSpec())
	_ = tk.MarkRunning("l1", 30*time.Second, now)

	outcome, err := tk.Fail("boom", now)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if outcome != FailRetry || tk.Status() != StatusPending {
		t.Fatalf("expected retry back to pending, got %s", tk.Status())
	}
	// 第二次执行并失败：重试额度耗尽。
	_ = tk.MarkRunning("l2", 30*time.Second, now)
	outcome, err = tk.Fail("boom again", now)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if outcome != FailFinal {
		t.Fatal("expected final failure")
	}
	if tk.Status() != StatusFailed {
		t.Fatalf("expected failed, got %s", tk.Status())
	}
}

func TestTaskLockExpiryAndRelease(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tk, _ := NewTask(newSpec())
	_ = tk.MarkRunning("l1", 30*time.Second, now)

	// 锁未到期不应回收。
	if requeued, _ := tk.ExpireLock(now.Add(10 * time.Second)); requeued {
		t.Fatal("lock should not expire yet")
	}
	if requeued, _ := tk.ExpireLock(now.Add(time.Minute)); !requeued {
		t.Fatal("lock should expire and requeue")
	}
	if tk.Status() != StatusPending {
		t.Fatalf("expected pending, got %s", tk.Status())
	}

	// 背压释放不应消耗重试次数。
	_ = tk.MarkRunning("l2", 30*time.Second, now)
	if err := tk.Release(now); err != nil {
		t.Fatalf("release: %v", err)
	}
	if tk.Attempts() != 1 {
		t.Fatalf("release should decrement attempts, got %d", tk.Attempts())
	}
}

func TestTaskTTLExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tk, _ := NewTask(newSpec())
	if tk.ExpireTTL(now.Add(time.Minute)) {
		t.Fatal("ttl should not expire yet")
	}
	if !tk.ExpireTTL(now.Add(10 * time.Minute)) {
		t.Fatal("ttl should expire")
	}
	if tk.Status() != StatusTimeout {
		t.Fatalf("expected timeout, got %s", tk.Status())
	}
}

func TestQueueFIFOAndCapacity(t *testing.T) {
	q := NewQueue("local-pc", 2)
	if err := q.Enqueue("t1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Enqueue("t2"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Enqueue("t3"); err == nil {
		t.Fatal("expected queue full error")
	}
	ids := q.Dequeue(2)
	if len(ids) != 2 || ids[0] != "t1" || ids[1] != "t2" {
		t.Fatalf("unexpected dequeue result: %v", ids)
	}
	q.Requeue("t1")
	q.Remove("t1")
	if q.Len() != 0 {
		t.Fatalf("expected empty queue, got %d", q.Len())
	}
}

func TestStatusTransitionTable(t *testing.T) {
	if CanTransition(StatusPending, StatusSuccess) {
		t.Fatal("pending should not jump to success")
	}
	if !CanTransition(StatusRunning, StatusSuccess) {
		t.Fatal("running should allow success")
	}
	if !CanTransition(StatusDeadLetter, StatusPending) {
		t.Fatal("dead letter should allow manual requeue")
	}
	if _, ok := ParseStatus("deadletter"); !ok {
		t.Fatal("parse deadletter failed")
	}
}

func TestParseMode(t *testing.T) {
	if ParseMode("x") != ModePull {
		t.Fatal("unknown mode should fallback to pull")
	}
	if ParseMode("direct") != ModeDirect {
		t.Fatal("expected direct")
	}
	if ParseMode("") != ModePull {
		t.Fatal("empty should fallback to pull")
	}
}
