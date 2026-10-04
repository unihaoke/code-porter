package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// AgentRegistry 网关侧的 Agent 注册与解析服务（多租户）。
//
// 身份模型：秘钥证明「归属」，实例 ID 区分「机器」。同一把秘钥可以在多台
// 机器上接入，每台机器有独立实例 ID，全部归属于秘钥属主；任务只允许在
// 属主自己的实例之间路由。
type AgentRegistry struct {
	repo      agent.Repository
	queueRepo task.TaskQueueRepository
	clock     port.Clock
	log       port.Logger
	policy    TaskPolicy
}

// NewAgentRegistry 构造注册服务。
func NewAgentRegistry(
	repo agent.Repository,
	queueRepo task.TaskQueueRepository,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *AgentRegistry {
	return &AgentRegistry{
		repo:      repo,
		queueRepo: queueRepo,
		clock:     clock,
		log:       log.With(port.F("svc", "agent_registry")),
		policy:    policy.withDefaults(),
	}
}

// AgentChoice 多实例选择清单条目（随 409 返回给调用方）。
type AgentChoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// AgentsMultiChoiceError 属主名下有多台实例且请求未指定目标（HTTP 409）。
type AgentsMultiChoiceError struct {
	cause   error
	Choices []AgentChoice
}

func newMultiChoiceError(choices []AgentChoice) *AgentsMultiChoiceError {
	return &AgentsMultiChoiceError{
		cause:   apperr.New(apperr.CodeConflict, "multiple agents available, please specify target agent"),
		Choices: choices,
	}
}

// Error 实现 error。
func (e *AgentsMultiChoiceError) Error() string { return e.cause.Error() }

// Unwrap 支持 errors.As/Is 提取 apperr.CodeConflict。
func (e *AgentsMultiChoiceError) Unwrap() error { return e.cause }

// errNoAgentForOwner 属主名下没有已注册实例（HTTP 503）。
var errNoAgentForOwner = apperr.New(apperr.CodeUnavailable, "no registered agent for this user")

// AuthenticateAndRegister 秘钥鉴权通过后注册/解析实例。
//
// ownerID 来自有效秘钥的属主；instanceID/name 来自客户端请求。
// 首次见到该实例则自动注册；实例已存在但归属他人 → 403（ID 碰撞或盗用）。
func (r *AgentRegistry) AuthenticateAndRegister(
	ctx context.Context,
	ownerID user.ID,
	instanceID agent.ID,
	name string,
) (*agent.Agent, error) {
	if ownerID == "" {
		return nil, apperr.New(apperr.CodeUnauthorized, "agent owner is required")
	}
	if instanceID == "" {
		return nil, apperr.New(apperr.CodeUnauthorized, "agent instance id is required")
	}

	a, err := r.repo.FindByIdentity(ctx, ownerID, instanceID)
	if err == nil {
		// 已注册：刷新机器名与最近出现时间，归属不变。
		a.UpdateName(name)
		a.SetLimits(r.policy.QueueMaxLen, 0)
		if err := r.repo.Save(ctx, a); err != nil {
			return nil, err
		}
		return a, nil
	}

	// 区分「他人同名实例（盗用）」与「确实不存在」。
	if existing, findErr := r.repo.Find(ctx, instanceID); findErr == nil && existing.OwnerID() != ownerID {
		return nil, agent.ErrOwnerMismatch
	}

	// 首次接入：注册实例并初始化其私有队列。
	a, err = agent.Register(agent.Spec{
		ID:      instanceID,
		OwnerID: ownerID,
		Name:    name,
		Now:     r.clock.Now(),
	})
	if err != nil {
		return nil, err
	}
	a.SetLimits(r.policy.QueueMaxLen, 0)
	if err := r.repo.Save(ctx, a); err != nil {
		return nil, err
	}
	if err := r.queueRepo.Ensure(ctx, instanceID, r.policy.QueueMaxLen); err != nil {
		return nil, err
	}
	r.log.Info("agent self-registered",
		port.F("user_id", string(ownerID)), port.F("agent", string(instanceID)))
	return a, nil
}

// ResolveForOwner 按租户解析目标实例（对外提交任务的唯一入口）。
//
// 显式指定：必须属于 owner，否则 not found（不泄露他人实例存在）。
// 未指定：0 台 → 503；恰 1 台 → 自动路由；多于 1 台 → 409 + 实例清单。
func (r *AgentRegistry) ResolveForOwner(ctx context.Context, ownerID user.ID, instanceID agent.ID) (*agent.Agent, error) {
	if instanceID != "" {
		a, err := r.repo.FindByIdentity(ctx, ownerID, instanceID)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeNotFound,
				"agent "+string(instanceID)+" is not registered for this user", err)
		}
		return a, nil
	}

	list, err := r.repo.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	switch len(list) {
	case 0:
		return nil, errNoAgentForOwner
	case 1:
		return list[0], nil
	default:
		choices := make([]AgentChoice, 0, len(list))
		for _, a := range list {
			choices = append(choices, AgentChoice{
				ID:     string(a.ID()),
				Name:   a.Name(),
				Status: a.Status().String(),
			})
		}
		return nil, newMultiChoiceError(choices)
	}
}

// Resolve 仅按实例 ID 解析（系统内部路径：ACK/心跳/超时扫描等已鉴权场景）。
func (r *AgentRegistry) Resolve(ctx context.Context, id agent.ID) (*agent.Agent, error) {
	a, err := r.repo.Find(ctx, id)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "agent "+string(id)+" is not registered", err)
	}
	return a, nil
}

// MarkOnline 标记 Agent 在线并刷新心跳。
func (r *AgentRegistry) MarkOnline(ctx context.Context, ownerID user.ID, id agent.ID) error {
	a, err := r.repo.FindByIdentity(ctx, ownerID, id)
	if err != nil {
		return err
	}
	a.MarkOnline(r.clock.Now())
	return r.repo.Save(ctx, a)
}

// MarkOffline 标记 Agent 离线（生命周期守护按全局扫描调用）。
func (r *AgentRegistry) MarkOffline(ctx context.Context, id agent.ID) error {
	a, err := r.Resolve(ctx, id)
	if err != nil {
		return nil // 已删除的 Agent 忽略
	}
	a.MarkOffline()
	return r.repo.Save(ctx, a)
}

// ListForOwner 列出某用户的实例。
func (r *AgentRegistry) ListForOwner(ctx context.Context, ownerID user.ID) ([]*agent.Agent, error) {
	return r.repo.ListByOwner(ctx, ownerID)
}

// List 列出全部实例（admin 视角）。
func (r *AgentRegistry) List(ctx context.Context) ([]*agent.Agent, error) {
	return r.repo.List(ctx)
}

// EvictOwner 删除用户后清理其名下实例身份，返回被清理的实例 ID
// （供上层关闭对应的 WebSocket 连接）。重复执行幂等。
// 队列里残留的在途任务按既有 TTL/留存策略自然过期，不做级联删除。
func (r *AgentRegistry) EvictOwner(ctx context.Context, ownerID user.ID) ([]agent.ID, error) {
	list, err := r.repo.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	ids := make([]agent.ID, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID())
		if err := r.repo.Delete(ctx, a.ID()); err != nil {
			return ids, err
		}
	}
	return ids, nil
}
