// Package task 是 CodePorter 的核心领域包，承载「任务」聚合根及其状态机。
//
// 任务状态机（PRD 5.1-4）：
//
//	pending ──拉取/加锁──> running ──成功──> success
//	   ^                     │
//	   │                     ├──失败(可重试)──┘
//	   │                     ├──失败(重试耗尽)──> deadletter
//	   │                     ├──锁超时──────────┐
//	   │                     └──本地背压释放────┘
//	   └─────────────────────────────────────────
//	pending ──TTL 到期──> timeout（终态）
package task

import (
	"strings"
	"time"

	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// ID 任务唯一标识。
type ID string

// String 返回字符串形式。
func (i ID) String() string { return string(i) }

// NewID 生成任务 ID。
func NewID() ID { return ID(id.New("task_")) }

// ErrInvalidTransition 非法状态迁移。
var ErrInvalidTransition = apperr.New(apperr.CodeConflict, "invalid task status transition")

// ErrNotRunning 任务未处于执行中，不接受片段/结果。
var ErrNotRunning = apperr.New(apperr.CodeConflict, "task is not running")

// ErrBadLockToken 任务锁校验失败。
var ErrBadLockToken = apperr.New(apperr.CodeConflict, "invalid task lock token")

// Spec 创建任务所需的全部输入。
type Spec struct {
	AgentID agent.ID
	// OwnerID 归属用户（租户隔离依据，必须与目标 Agent 的属主一致）。
	OwnerID     user.ID
	Model       model.Model
	Request     Request
	Mode        DeliveryMode
	Stream      bool
	MaxRetry    int
	LockTimeout time.Duration
	TTL         time.Duration
	APIKeyID    string
	Now         time.Time
}

// Task 任务聚合根。所有状态变更必须通过聚合方法，保证不变式成立。
type Task struct {
	id          ID
	agentID     agent.ID
	ownerID     user.ID
	model       model.Model
	request     Request
	mode        DeliveryMode
	stream      bool
	status      Status
	attempts    int
	maxRetry    int
	lockToken   string
	lockedUntil time.Time
	createdAt   time.Time
	updatedAt   time.Time
	startedAt   time.Time
	finishedAt  time.Time
	expiresAt   time.Time
	chunks      []Chunk
	result      string
	errMessage  string
	apiKeyID    string
	seq         int
	// lockTimeout 单次执行的任务锁时长，用于重试时重新计算。
	lockTimeout time.Duration
	// ttl 任务整体存活时长。
	ttl time.Duration
}

// NewTask 创建处于 pending 的任务。
func NewTask(spec Spec) (*Task, error) {
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	if spec.AgentID == "" {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "agent id is required", nil)
	}
	if spec.OwnerID == "" {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "task owner is required", nil)
	}
	if spec.Model == "" {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "model is required", nil)
	}
	if strings.TrimSpace(spec.Request.FlattenPrompt()) == "" {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "prompt is empty", nil)
	}
	maxRetry := spec.MaxRetry
	if maxRetry < 0 {
		maxRetry = 0
	}
	lockTimeout := spec.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = 60 * time.Second
	}
	ttl := spec.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Task{
		id:          NewID(),
		agentID:     spec.AgentID,
		ownerID:     spec.OwnerID,
		model:       spec.Model,
		request:     spec.Request,
		mode:        spec.Mode,
		stream:      spec.Stream,
		status:      StatusPending,
		maxRetry:    maxRetry,
		createdAt:   now,
		updatedAt:   now,
		expiresAt:   now.Add(ttl),
		apiKeyID:    spec.APIKeyID,
		lockTimeout: lockTimeout,
		ttl:         ttl,
	}, nil
}

// --- 只读访问器 ---

// ID 任务 ID。
func (t *Task) ID() ID { return t.id }

// AgentID 归属 Agent。
func (t *Task) AgentID() agent.ID { return t.agentID }

// OwnerID 归属用户（租户）。
func (t *Task) OwnerID() user.ID { return t.ownerID }

// Model 目标本地 AI 工具。
func (t *Task) Model() model.Model { return t.model }

// Request 标准化入参。
func (t *Task) Request() Request { return t.request }

// Mode 投递通路。
func (t *Task) Mode() DeliveryMode { return t.mode }

// Stream 是否需要流式输出。
func (t *Task) Stream() bool { return t.stream }

// Status 当前状态。
func (t *Task) Status() Status { return t.status }

// Attempts 已执行次数。
func (t *Task) Attempts() int { return t.attempts }

// MaxRetry 最大重试次数。
func (t *Task) MaxRetry() int { return t.maxRetry }

// LockToken 当前任务锁令牌。
func (t *Task) LockToken() string { return t.lockToken }

// LockedUntil 任务锁到期时间。
func (t *Task) LockedUntil() time.Time { return t.lockedUntil }

// ExpiresAt TTL 到期时间。
func (t *Task) ExpiresAt() time.Time { return t.expiresAt }

// CreatedAt 创建时间。
func (t *Task) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt 最近更新时间。
func (t *Task) UpdatedAt() time.Time { return t.updatedAt }

// StartedAt 首次开始执行时间。
func (t *Task) StartedAt() time.Time { return t.startedAt }

// FinishedAt 结束时间（终态才有意义）。
func (t *Task) FinishedAt() time.Time { return t.finishedAt }

// ErrorMessage 错误信息。
func (t *Task) ErrorMessage() string { return t.errMessage }

// APIKeyID 发起方 API Key 标识，用于审计与限流。
func (t *Task) APIKeyID() string { return t.apiKeyID }

// Seq 当前片段序号。
func (t *Task) Seq() int { return t.seq }

// Chunks 返回片段副本，避免外部修改聚合内部状态。
func (t *Task) Chunks() []Chunk {
	out := make([]Chunk, len(t.chunks))
	copy(out, t.chunks)
	return out
}

// Result 返回最终结果文本：优先取显式结果，否则拼接全部片段。
func (t *Task) Result() string {
	if t.result != "" {
		return t.result
	}
	return Chunks(t.chunks).Text()
}

// Clone 返回任务副本。
//
// 内存仓储以「取副本 → 修改 → 回写」的方式工作，
// 避免把聚合内部状态直接暴露给并发的调用方造成数据竞争。
func (t *Task) Clone() *Task {
	cp := *t
	if len(t.chunks) > 0 {
		cp.chunks = make([]Chunk, len(t.chunks))
		copy(cp.chunks, t.chunks)
	}
	return &cp
}

// --- 状态迁移 ---

func (t *Task) transit(to Status, now time.Time) error {
	if !CanTransition(t.status, to) {
		return apperr.Wrap(apperr.CodeConflict,
			"cannot transit task from "+string(t.status)+" to "+string(to), ErrInvalidTransition)
	}
	t.status = to
	t.updatedAt = now
	if to.IsTerminal() {
		t.finishedAt = now
		t.lockToken = ""
	}
	return nil
}

// MarkRunning 被 Agent 拉取：pending → running，并加任务锁防止重复消费。
func (t *Task) MarkRunning(lockToken string, lockTimeout time.Duration, now time.Time) error {
	if t.status != StatusPending {
		return apperr.Wrap(apperr.CodeConflict, "task is not pending", ErrInvalidTransition)
	}
	if lockToken == "" {
		return apperr.Wrap(apperr.CodeInvalidParam, "lock token is required", nil)
	}
	if lockTimeout <= 0 {
		lockTimeout = t.lockTimeout
	}
	if err := t.transit(StatusRunning, now); err != nil {
		return err
	}
	t.lockToken = lockToken
	t.lockedUntil = now.Add(lockTimeout)
	t.attempts++
	if t.startedAt.IsZero() {
		t.startedAt = now
	}
	return nil
}

// RenewLock 续租任务锁（长任务心跳续期）。
func (t *Task) RenewLock(lockToken string, lockTimeout time.Duration, now time.Time) error {
	if err := t.validateLock(lockToken); err != nil {
		return err
	}
	if lockTimeout <= 0 {
		lockTimeout = t.lockTimeout
	}
	t.lockedUntil = now.Add(lockTimeout)
	t.updatedAt = now
	return nil
}

func (t *Task) validateLock(lockToken string) error {
	if t.status != StatusRunning {
		return ErrNotRunning
	}
	if t.lockToken == "" || lockToken == "" || t.lockToken != lockToken {
		return ErrBadLockToken
	}
	return nil
}

// AppendChunk 追加一段流式输出，仅 running 状态允许。
func (t *Task) AppendChunk(content string, now time.Time) (Chunk, error) {
	if t.status != StatusRunning {
		return Chunk{}, ErrNotRunning
	}
	t.seq++
	c := Chunk{Seq: t.seq, Content: content, At: now}
	t.chunks = append(t.chunks, c)
	t.updatedAt = now
	return c, nil
}

// Complete 正常结束：running → success。
func (t *Task) Complete(result string, now time.Time) error {
	if err := t.transit(StatusSuccess, now); err != nil {
		return err
	}
	t.result = result
	if t.result == "" {
		t.result = Chunks(t.chunks).Text()
	}
	t.errMessage = ""
	return nil
}

// FailOutcome 失败处理结果。
type FailOutcome int

const (
	// FailFinal 已达重试上限，任务进入终态失败/死信。
	FailFinal FailOutcome = iota
	// FailRetry 仍有重试额度，任务回到 pending 等待重新调度。
	FailRetry
)

// Fail 执行失败：运行态 →（可重试则回 pending，否则 failed/deadletter）。
func (t *Task) Fail(reason string, now time.Time) (FailOutcome, error) {
	if t.status != StatusRunning {
		return FailFinal, ErrNotRunning
	}
	t.errMessage = reason
	if t.attempts <= t.maxRetry {
		if err := t.transit(StatusPending, now); err != nil {
			return FailFinal, err
		}
		t.lockToken = ""
		t.lockedUntil = time.Time{}
		// 重新给一次完整的 TTL，避免重试任务刚入队就过期。
		t.expiresAt = now.Add(t.ttl)
		return FailRetry, nil
	}
	if err := t.transit(StatusFailed, now); err != nil {
		return FailFinal, err
	}
	return FailFinal, nil
}

// ToDeadLetter 直接进入死信（重试彻底耗尽）。
func (t *Task) ToDeadLetter(reason string, now time.Time) error {
	t.errMessage = reason
	return t.transit(StatusDeadLetter, now)
}

// Release 本地背压释放：Agent 拉取后发现本地协程池已满，主动放弃消费。
// 任务回到 pending，且不消耗执行次数；TTL 仍然生效，避免死循环占用队列。
func (t *Task) Release(now time.Time) error {
	if t.status != StatusRunning {
		return ErrNotRunning
	}
	if err := t.transit(StatusPending, now); err != nil {
		return err
	}
	if t.attempts > 0 {
		t.attempts--
	}
	t.lockToken = ""
	t.lockedUntil = time.Time{}
	return nil
}

// ExpireLock 任务锁超时：Agent 宕机/卡死时回收任务。
// 返回 true 表示任务已回到 pending 可重新调度；false 表示已转入死信。
func (t *Task) ExpireLock(now time.Time) (bool, error) {
	if t.status != StatusRunning {
		return false, nil
	}
	if now.Before(t.lockedUntil) {
		return false, nil
	}
	reason := "task lock timeout (agent may be offline or stuck)"
	t.errMessage = reason
	if t.attempts > t.maxRetry {
		return false, t.ToDeadLetter(reason, now)
	}
	if err := t.transit(StatusPending, now); err != nil {
		return false, err
	}
	t.lockToken = ""
	t.lockedUntil = time.Time{}
	return true, nil
}

// ExpireTTL 任务 TTL 到期：pending 任务无人消费 → 终态 timeout。
func (t *Task) ExpireTTL(now time.Time) bool {
	if t.status.IsTerminal() {
		return false
	}
	if now.Before(t.expiresAt) {
		return false
	}
	t.errMessage = "task ttl expired"
	if t.status == StatusRunning {
		// 执行中超时：先释放锁再标记终态。
		t.lockToken = ""
	}
	_ = t.transit(StatusTimeout, now)
	return true
}

// Requeue 运维/手动重投：终态任务重新回到 pending。
func (t *Task) Requeue(now time.Time) error {
	if !t.status.IsTerminal() {
		return apperr.Wrap(apperr.CodeConflict, "only terminal task can be requeued", ErrInvalidTransition)
	}
	if err := t.transit(StatusPending, now); err != nil {
		return err
	}
	t.attempts = 0
	t.errMessage = ""
	t.result = ""
	t.chunks = nil
	t.seq = 0
	t.expiresAt = now.Add(t.ttl)
	return nil
}

// ErrTaskNotFound 任务不存在。
var ErrTaskNotFound = apperr.New(apperr.CodeNotFound, "task not found")
