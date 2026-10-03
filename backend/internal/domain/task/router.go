package task

import "github.com/codeporter/code-porter/pkg/apperr"

// RoutingDecision 任务路由裁决结果。
type RoutingDecision struct {
	// Mode 最终生效的投递通路。
	Mode DeliveryMode
	// Downgraded 是否发生了降级（请求 direct 但不具备条件）。
	Downgraded bool
	// Reason 决策原因，用于日志与响应头。
	Reason string
}

// Router 领域服务：根据请求的通路偏好与 Agent 实时状态裁决实际投递方式。
//
// 规则（PRD 3.2 / 风险清单）：
//   - direct 模式强依赖长连接：Agent 未建立 WebSocket 时直接失败（不允许静默降级，
//     否则调用方会误以为交互式请求已进入低延迟链路）；
//   - pull 模式允许 Agent 离线：任务留在网关队列，Agent 上线后继续消费，断连不丢任务。
type Router struct{}

// NewRouter 构造路由器。
func NewRouter() Router { return Router{} }

// Route 裁决通路。
func (Router) Route(requested DeliveryMode, wsConnected bool) (RoutingDecision, error) {
	if requested == ModeDirect {
		if !wsConnected {
			return RoutingDecision{}, apperr.New(apperr.CodeNotConnected,
				"direct mode requires an alive agent websocket connection")
		}
		return RoutingDecision{Mode: ModeDirect, Reason: "agent websocket connected"}, nil
	}
	if wsConnected {
		return RoutingDecision{Mode: ModePull, Reason: "pull mode"}, nil
	}
	return RoutingDecision{Mode: ModePull, Reason: "pull mode (agent offline, task queued)"}, nil
}
