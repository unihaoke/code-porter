package task

import "strings"

// Status 任务状态。
type Status string

const (
	// StatusPending 已入队，等待 Agent 拉取。
	StatusPending Status = "pending"
	// StatusRunning 已被 Agent 拉取并加锁，执行中。
	StatusRunning Status = "running"
	// StatusSuccess 执行成功。
	StatusSuccess Status = "success"
	// StatusFailed 执行失败（已耗尽重试或明确失败）。
	StatusFailed Status = "failed"
	// StatusTimeout 任务超时（TTL 到期或本地执行超时）。
	StatusTimeout Status = "timeout"
	// StatusDeadLetter 死信：多次重试仍失败，不再自动调度。
	StatusDeadLetter Status = "deadletter"
)

// String 返回状态字符串。
func (s Status) String() string { return string(s) }

// AllStatuses 返回全部状态，供运维接口做全量统计。
func AllStatuses() []Status {
	return []Status{
		StatusPending,
		StatusRunning,
		StatusSuccess,
		StatusFailed,
		StatusTimeout,
		StatusDeadLetter,
	}
}

// IsTerminal 是否为终态：终态任务不再参与调度。
func (s Status) IsTerminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusTimeout, StatusDeadLetter:
		return true
	}
	return false
}

// ParseStatus 解析状态字符串，未知返回 StatusPending 与 false。
func ParseStatus(s string) (Status, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return StatusPending, true
	case "running":
		return StatusRunning, true
	case "success", "succeeded":
		return StatusSuccess, true
	case "failed", "failure":
		return StatusFailed, true
	case "timeout":
		return StatusTimeout, true
	case "deadletter", "dead_letter":
		return StatusDeadLetter, true
	}
	return StatusPending, false
}

// Transition 状态机：校验 from → to 是否允许。
var Transition = map[Status][]Status{
	StatusPending:    {StatusRunning, StatusTimeout, StatusDeadLetter},
	StatusRunning:    {StatusSuccess, StatusFailed, StatusTimeout, StatusPending, StatusDeadLetter},
	StatusSuccess:    {},
	StatusFailed:     {StatusPending}, // 允许人工/运维重新入队
	StatusTimeout:    {StatusPending},
	StatusDeadLetter: {StatusPending},
}

// CanTransition 判断状态迁移是否合法。
func CanTransition(from, to Status) bool {
	for _, s := range Transition[from] {
		if s == to {
			return true
		}
	}
	return false
}
