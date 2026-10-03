package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// ReportHealthCommand 本机健康上报命令（PRD 5.2-10）。
type ReportHealthCommand struct {
	AgentID agent.ID
	Health  agent.Health
}

// ReportHealthUseCase 接收 LocalAgent 的健康上报，供网关提前失败与可观测性使用。
type ReportHealthUseCase struct {
	registry *AgentRegistry
	clock    port.Clock
	log      port.Logger
	policy   TaskPolicy
}

// NewReportHealthUseCase 构造用例。
func NewReportHealthUseCase(
	registry *AgentRegistry,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
) *ReportHealthUseCase {
	return &ReportHealthUseCase{
		registry: registry,
		clock:    clock,
		log:      log.With(port.F("uc", "report_health")),
		policy:   policy.withDefaults(),
	}
}

// Execute 记录健康快照，并按本地协程池容量标记 Agent 是否忙碌。
func (u *ReportHealthUseCase) Execute(ctx context.Context, cmd ReportHealthCommand) error {
	ag, err := u.registry.Resolve(ctx, cmd.AgentID)
	if err != nil {
		return err
	}
	now := u.clock.Now()
	ag.UpdateHealth(cmd.Health, now)
	ag.SetLimits(u.policy.QueueMaxLen, cmd.Health.MaxConcurrency)
	if cmd.Health.IsOverload() {
		ag.MarkBusy(now)
	} else if ag.Status() != agent.StatusOffline {
		ag.MarkOnline(now)
	}
	if err := u.registry.repo.Save(ctx, ag); err != nil {
		return apperr.Wrap(apperr.CodeInternal, "save agent health failed", err)
	}
	u.log.Debug("health reported",
		port.F("agent", string(cmd.AgentID)),
		port.F("cpu", cmd.Health.CPUPercent),
		port.F("mem", cmd.Health.MemPercent),
		port.F("inflight", cmd.Health.Inflight),
		port.F("queued", cmd.Health.Queued),
		port.F("overload", cmd.Health.IsOverload()))
	return nil
}
