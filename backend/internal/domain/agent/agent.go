// Package agent 承载「本地代理节点」聚合根：注册信息、在线状态与健康度。
//
// Agent 不暴露任何本地端口，只主动向外连接网关（PRD 安全要求）。
package agent

import (
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// ID Agent 唯一标识。
type ID string

// String 返回字符串形式。
func (i ID) String() string { return string(i) }

// NewID 生成 Agent ID。
func NewID() ID { return ID(id.New("agent_")) }

// Status 在线状态。
type Status string

const (
	// StatusOnline 在线（心跳正常）。
	StatusOnline Status = "online"
	// StatusOffline 离线（心跳超时或未连接）。
	StatusOffline Status = "offline"
	// StatusBusy 在线但本地协程池饱和。
	StatusBusy Status = "busy"
)

// String 返回状态字符串。
func (s Status) String() string { return string(s) }

// ErrAgentNotFound Agent 不存在。
var ErrAgentNotFound = apperr.New(apperr.CodeNotFound, "agent not found")

// ErrOwnerRequired Agent 必须归属某个用户。
var ErrOwnerRequired = apperr.New(apperr.CodeInvalidParam, "agent owner is required")

// ErrOwnerMismatch 实例归属与凭据属主不一致（实例 ID 碰撞或盗用）。
var ErrOwnerMismatch = apperr.New(apperr.CodeForbidden, "agent instance belongs to another user")

// Spec 注册 Agent 实例的输入。
type Spec struct {
	ID    ID
	// OwnerID 归属用户（秘钥属主）。
	OwnerID user.ID
	Name  string
	Now   time.Time
	// HeartbeatTimeout 心跳超时时长，超过则判定离线。
	HeartbeatTimeout time.Duration
}

// Agent 本地代理聚合根。
type Agent struct {
	id               ID
	ownerID          user.ID
	name             string
	status           Status
	registeredAt     time.Time
	lastHeartbeatAt  time.Time
	heartbeatTimeout time.Duration
	health           Health
	// QueueMaxLen 该 Agent 私有队列最大长度。
	queueMaxLen int
	// MaxConcurrency 本地协程池硬上限（由 Agent 上报，网关用于展示与限流参考）。
	maxConcurrency int
}

// Register 创建 Agent 实例（首次自注册），初始状态为 offline。
func Register(spec Spec) (*Agent, error) {
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	if spec.ID == "" {
		return nil, apperr.Wrap(apperr.CodeInvalidParam, "agent instance id is required", nil)
	}
	if spec.OwnerID == "" {
		return nil, ErrOwnerRequired
	}
	hb := spec.HeartbeatTimeout
	if hb <= 0 {
		hb = 30 * time.Second
	}
	return &Agent{
		id:               spec.ID,
		ownerID:          spec.OwnerID,
		name:             spec.Name,
		status:           StatusOffline,
		registeredAt:     now,
		lastHeartbeatAt:  now,
		heartbeatTimeout: hb,
	}, nil
}

// Clone 返回副本（内存仓储以「取副本 → 修改 → 回写」方式避免数据竞争）。
func (a *Agent) Clone() *Agent {
	cp := *a
	if len(a.health.MCPs) > 0 {
		cp.health.MCPs = append([]MCPHealth(nil), a.health.MCPs...)
	}
	return &cp
}

// ID 标识。
func (a *Agent) ID() ID { return a.id }

// OwnerID 归属用户 ID（租户隔离依据）。
func (a *Agent) OwnerID() user.ID { return a.ownerID }

// Name 名称。
func (a *Agent) Name() string { return a.name }

// Status 在线状态。
func (a *Agent) Status() Status { return a.status }

// RegisteredAt 注册时间。
func (a *Agent) RegisteredAt() time.Time { return a.registeredAt }

// LastHeartbeatAt 最近心跳时间。
func (a *Agent) LastHeartbeatAt() time.Time { return a.lastHeartbeatAt }

// Health 最近一次健康快照。
func (a *Agent) Health() Health { return a.health }

// QueueMaxLen 私有队列上限。
func (a *Agent) QueueMaxLen() int { return a.queueMaxLen }

// MaxConcurrency 本地协程池上限。
func (a *Agent) MaxConcurrency() int { return a.maxConcurrency }

// VerifyOwner 校验实例归属；归属不一致返回 ErrOwnerMismatch。
func (a *Agent) VerifyOwner(ownerID user.ID) error {
	if ownerID == "" || a.ownerID != ownerID {
		return ErrOwnerMismatch
	}
	return nil
}

// UpdateName 更新机器名（Agent 重连时上报最新主机名/备注名）。
func (a *Agent) UpdateName(name string) {
	if name = strings.TrimSpace(name); name != "" {
		a.name = name
	}
}

// RewriteIdentity 仓储反序列化回填持久身份，仅供仓储使用。
//
// 在线状态/健康快照/队列容量是易失运行时态，不从持久化层恢复：
// 重连后由心跳与健康上报重建，因此重建实例初始为 offline。
func (a *Agent) RewriteIdentity(id ID, ownerID user.ID, name string, createdAt, lastSeenAt time.Time) {
	a.id = id
	a.ownerID = ownerID
	a.name = name
	a.registeredAt = createdAt
	a.lastHeartbeatAt = lastSeenAt
	a.status = StatusOffline
}

// MarkOnline 标记在线并刷新心跳。
func (a *Agent) MarkOnline(now time.Time) {
	a.status = StatusOnline
	a.lastHeartbeatAt = now
}

// MarkBusy 标记忙碌（本地协程池饱和）。
func (a *Agent) MarkBusy(now time.Time) {
	a.status = StatusBusy
	a.lastHeartbeatAt = now
}

// MarkOffline 标记离线（长连接断开或心跳超时）。
func (a *Agent) MarkOffline() { a.status = StatusOffline }

// UpdateHealth 上报本机健康状态。
func (a *Agent) UpdateHealth(h Health, now time.Time) {
	h.UpdatedAt = now
	a.health = h
	a.lastHeartbeatAt = now
	if a.status == StatusOffline {
		a.status = StatusOnline
	}
}

// SetLimits 设置队列上限与本地并发上限。
func (a *Agent) SetLimits(queueMaxLen, maxConcurrency int) {
	a.queueMaxLen = queueMaxLen
	a.maxConcurrency = maxConcurrency
}

// IsAvailable 是否可接收新任务：在线或忙碌均可（pull 模式任务会排队）。
func (a *Agent) IsAvailable() bool { return a.status == StatusOnline || a.status == StatusBusy }

// HeartbeatExpired 心跳是否已超时。
func (a *Agent) HeartbeatExpired(now time.Time) bool {
	if a.status == StatusOffline {
		return false
	}
	return now.Sub(a.lastHeartbeatAt) > a.heartbeatTimeout
}

// SweepHeartbeat 心跳超时裁决：超过阈值则置为离线。
func (a *Agent) SweepHeartbeat(now time.Time) bool {
	if a.HeartbeatExpired(now) {
		a.status = StatusOffline
		return true
	}
	return false
}
