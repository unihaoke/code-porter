package agent

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/pkg/pool"
)

// HealthReporter 定时采集本机状态并上报网关（PRD 5.2-10）。
//
// 网关依据上报结果提前失败：本地 AI 软件没启动时，不再让请求无限等待。
type HealthReporter struct {
	client    port.GatewayClient
	agentID   agent.ID
	probe     SystemProbe
	mcpProber MCPProber
	p         *pool.Pool
	log       port.Logger
	policy    Policy
}

// NewHealthReporter 构造健康上报器。
func NewHealthReporter(
	client port.GatewayClient,
	agentID agent.ID,
	probe SystemProbe,
	mcpProber MCPProber,
	p *pool.Pool,
	log port.Logger,
	policy Policy,
) *HealthReporter {
	return &HealthReporter{
		client:    client,
		agentID:   agentID,
		probe:     probe,
		mcpProber: mcpProber,
		p:         p,
		log:       log.With(port.F("svc", "health_reporter")),
		policy:    policy.withDefaults(),
	}
}

// Run 启动定时上报，随 ctx 取消退出。
func (h *HealthReporter) Run(ctx context.Context) {
	h.log.Info("health reporter started", port.F("interval", h.policy.HeartbeatInterval.String()))
	ticker := time.NewTicker(h.policy.HeartbeatInterval)
	defer ticker.Stop()
	h.report(ctx) // 启动即上报一次，便于网关快速感知上线
	for {
		select {
		case <-ctx.Done():
			h.log.Info("health reporter stopped")
			return
		case <-ticker.C:
			h.report(ctx)
		}
	}
}

func (h *HealthReporter) report(ctx context.Context) {
	mcps := h.collectMCPHealth(ctx)
	stats := h.p.Stats()

	health, err := h.probe.Probe(ctx, stats.Inflight, stats.Queued, stats.MaxConcurrency, mcps)
	if err != nil {
		h.log.Warn("probe system failed", port.F("err", err.Error()))
		return
	}
	if err := h.client.ReportHealth(ctx, string(h.agentID), health); err != nil {
		h.log.Warn("report health failed", port.F("err", err.Error()))
		return
	}
	h.log.Debug("health reported",
		port.F("cpu", health.CPUPercent),
		port.F("mem", health.MemPercent),
		port.F("inflight", health.Inflight),
		port.F("queued", health.Queued))
}

func (h *HealthReporter) collectMCPHealth(ctx context.Context) []agent.MCPHealth {
	if h.mcpProber == nil {
		return nil
	}
	items := h.mcpProber.Health(ctx)
	out := make([]agent.MCPHealth, 0, len(items))
	for _, it := range items {
		out = append(out, agent.MCPHealth{Model: it.Model, Available: it.Available, Detail: it.Detail})
	}
	return out
}
