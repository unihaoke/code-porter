package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// AgentRegistry 网关侧的 Agent 注册与解析服务。
//
// MVP 为单用户单 Agent（一个网关绑定一台本地 PC），
// 但仓储接口已按多 Agent 设计，V0.3 可直接扩展为多节点负载均衡。
type AgentRegistry struct {
	repo           agent.Repository
	queueRepo      task.TaskQueueRepository
	clock          port.Clock
	log            port.Logger
	defaultAgentID agent.ID
	policy         TaskPolicy
}

// NewAgentRegistry 构造注册服务。
func NewAgentRegistry(
	repo agent.Repository,
	queueRepo task.TaskQueueRepository,
	clock port.Clock,
	log port.Logger,
	defaultAgentID agent.ID,
	policy TaskPolicy,
) *AgentRegistry {
	return &AgentRegistry{
		repo:           repo,
		queueRepo:      queueRepo,
		clock:          clock,
		log:            log.With(port.F("svc", "agent_registry")),
		defaultAgentID: defaultAgentID,
		policy:         policy.withDefaults(),
	}
}

// DefaultAgentID 返回配置的默认 Agent。
func (r *AgentRegistry) DefaultAgentID() agent.ID { return r.defaultAgentID }

// Ensure 注册或更新 Agent，并初始化其私有队列。
func (r *AgentRegistry) Ensure(ctx context.Context, id agent.ID, name, token string) (*agent.Agent, error) {
	if id == "" {
		id = r.defaultAgentID
	}
	a, err := r.repo.Find(ctx, id)
	if err != nil {
		// 首次注册：创建聚合根并持久化。
		a, err = agent.NewAgent(agent.Spec{ID: id, Name: name, Token: token, Now: r.clock.Now()})
		if err != nil {
			return nil, err
		}
	}
	// 队列上限由网关策略统一管控，避免 Agent 侧谎报容量。
	a.SetLimits(r.policy.QueueMaxLen, 0)
	if err := r.repo.Save(ctx, a); err != nil {
		return nil, err
	}
	if err := r.queueRepo.Ensure(ctx, id, r.policy.QueueMaxLen); err != nil {
		return nil, err
	}
	return a, nil
}

// Resolve 解析 Agent；id 为空时使用默认 Agent。
func (r *AgentRegistry) Resolve(ctx context.Context, id agent.ID) (*agent.Agent, error) {
	if id == "" {
		id = r.defaultAgentID
	}
	a, err := r.repo.Find(ctx, id)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "agent "+string(id)+" is not registered", err)
	}
	return a, nil
}

// Authenticate 按 Agent Token 鉴权（Pull / ACK / WS 接口使用）。
func (r *AgentRegistry) Authenticate(ctx context.Context, token string) (*agent.Agent, error) {
	if token == "" {
		return nil, apperr.New(apperr.CodeUnauthorized, "agent token is required")
	}
	a, err := r.repo.FindByToken(ctx, token)
	if err != nil {
		return nil, apperr.New(apperr.CodeUnauthorized, "invalid agent token")
	}
	return a, nil
}

// MarkOnline 标记 Agent 在线并刷新心跳。
func (r *AgentRegistry) MarkOnline(ctx context.Context, id agent.ID) error {
	a, err := r.Resolve(ctx, id)
	if err != nil {
		return err
	}
	a.MarkOnline(r.clock.Now())
	return r.repo.Save(ctx, a)
}

// MarkOffline 标记 Agent 离线。
func (r *AgentRegistry) MarkOffline(ctx context.Context, id agent.ID) error {
	a, err := r.repo.Find(ctx, id)
	if err != nil {
		return nil // 已删除，忽略
	}
	a.MarkOffline()
	return r.repo.Save(ctx, a)
}

// List 列出全部 Agent。
func (r *AgentRegistry) List(ctx context.Context) ([]*agent.Agent, error) {
	return r.repo.List(ctx)
}
