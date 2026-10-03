// Package system 采集本机资源信息，供 LocalAgent 健康上报使用。
package system

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/codeporter/code-porter/internal/domain/agent"
)

// Probe 本机资源探测器，实现 agentapp.SystemProbe 所需签名。
type Probe struct {
	mu       sync.Mutex
	hostname string
	os       string
	// lastCPUSample 上一次 CPU 采样，用于计算真实使用率。
	lastCPUSample  time.Time
	lastCPUPercent float64
}

// NewProbe 构造探测器。
func NewProbe() *Probe {
	hostname, _ := os.Hostname()
	return &Probe{hostname: hostname, os: runtime.GOOS}
}

// Probe 采集一次健康快照。
func (p *Probe) Probe(_ context.Context, inflight, queued, maxConcurrency int, mcps []agent.MCPHealth) (agent.Health, error) {
	return agent.Health{
		CPUPercent:     p.cpuPercent(),
		MemPercent:     p.memPercent(),
		Inflight:       inflight,
		Queued:         queued,
		MaxConcurrency: maxConcurrency,
		MCPs:           mcps,
		Hostname:       p.hostname,
		OS:             p.os,
		UpdatedAt:      time.Now(),
	}, nil
}

func (p *Probe) cpuPercent() float64 {
	// 采样 200ms，得到真实使用率（gopsutil 内部做差值计算）。
	percents, err := cpu.Percent(200*time.Millisecond, false)
	if err != nil || len(percents) == 0 {
		return 0
	}
	v := percents[0]
	p.mu.Lock()
	p.lastCPUPercent = v
	p.lastCPUSample = time.Now()
	p.mu.Unlock()
	return v
}

func (p *Probe) memPercent() float64 {
	stat, err := mem.VirtualMemory()
	if err != nil {
		return 0
	}
	return stat.UsedPercent
}

// HostInfo 返回主机信息（用于启动日志）。
func (p *Probe) HostInfo() (string, string) {
	info, err := host.Info()
	if err != nil {
		return p.hostname, p.os
	}
	return info.Hostname, info.OS + "/" + info.Platform
}
