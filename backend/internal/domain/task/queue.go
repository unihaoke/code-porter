package task

import (
	"strconv"

	"github.com/codeporter/code-porter/pkg/apperr"

	"github.com/codeporter/code-porter/internal/domain/agent"
)

// DefaultQueueMaxLen 单 Agent 私有队列默认最大长度。
const DefaultQueueMaxLen = 512

// Queue 单个 Agent 的私有任务队列（FIFO）。
//
// 队列长度上限是关键的背压与限流手段：队列满时网关直接拒绝新任务（429），
// 防止海量请求把本地 PC 打爆（PRD 8 风险清单）。
type Queue struct {
	agentID agent.ID
	maxLen  int
	ids     []ID
}

// NewQueue 构造队列，maxLen<=0 使用 DefaultQueueMaxLen。
func NewQueue(agentID agent.ID, maxLen int) *Queue {
	if maxLen <= 0 {
		maxLen = DefaultQueueMaxLen
	}
	return &Queue{agentID: agentID, maxLen: maxLen, ids: make([]ID, 0, 16)}
}

// AgentID 归属 Agent。
func (q *Queue) AgentID() agent.ID { return q.agentID }

// MaxLen 队列容量上限。
func (q *Queue) MaxLen() int { return q.maxLen }

// Len 当前长度。
func (q *Queue) Len() int { return len(q.ids) }

// Full 是否已满。
func (q *Queue) Full() bool { return len(q.ids) >= q.maxLen }

// Enqueue 入队；已满返回 apperr.CodeQueueFull。
func (q *Queue) Enqueue(id ID) error {
	if q.Full() {
		return apperr.New(apperr.CodeQueueFull,
			"agent "+string(q.agentID)+" queue is full ("+strconv.Itoa(q.maxLen)+")")
	}
	q.ids = append(q.ids, id)
	return nil
}

// Requeue 重新入队（任务锁超时回收 / 本地背压释放），不受容量限制——任务本来就持有名额。
func (q *Queue) Requeue(id ID) {
	if q.contains(id) {
		return
	}
	q.ids = append(q.ids, id)
}

// Dequeue 出队最多 n 个任务 ID；n<=0 返回空。
func (q *Queue) Dequeue(n int) []ID {
	if n <= 0 || len(q.ids) == 0 {
		return nil
	}
	if n > len(q.ids) {
		n = len(q.ids)
	}
	out := make([]ID, n)
	copy(out, q.ids[:n])
	q.ids = q.ids[n:]
	return out
}

// Remove 移除指定任务（终态清理）。
func (q *Queue) Remove(id ID) {
	for i, cur := range q.ids {
		if cur == id {
			q.ids = append(q.ids[:i], q.ids[i+1:]...)
			return
		}
	}
}

// Peek 返回前 n 个任务 ID 但不移除。
func (q *Queue) Peek(n int) []ID {
	if n <= 0 || len(q.ids) == 0 {
		return nil
	}
	if n > len(q.ids) {
		n = len(q.ids)
	}
	out := make([]ID, n)
	copy(out, q.ids[:n])
	return out
}

func (q *Queue) contains(id ID) bool {
	for _, cur := range q.ids {
		if cur == id {
			return true
		}
	}
	return false
}
