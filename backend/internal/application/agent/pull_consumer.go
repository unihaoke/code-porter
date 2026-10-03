package agent

import (
	"context"
	"errors"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/pkg/backoff"
	"github.com/codeporter/code-porter/pkg/pool"
)

// PullConsumer Pull 队列模式的消费者：定时拉取 → 投递本地协程池 → 动态退避。
//
// 背压实现（PRD 5.2-3）：
//   - 本地协程池剩余容量为 0 时立即停止拉取并拉长轮询间隔；
//   - 拉取到却无法投递的任务，主动上报 released 归还网关队列，
//     而不是等任务锁超时（60s）才回收，显著降低重试延迟。
type PullConsumer struct {
	client   port.GatewayClient
	agentID  agent.ID
	pool     *pool.Pool
	executor *TaskExecutor
	reporter ResultReporter
	backoff  *backoff.Dynamic
	clock    port.Clock
	log      port.Logger
	policy   Policy
}

// NewPullConsumer 构造消费者。
func NewPullConsumer(
	client port.GatewayClient,
	agentID agent.ID,
	p *pool.Pool,
	executor *TaskExecutor,
	reporter ResultReporter,
	clock port.Clock,
	log port.Logger,
	policy Policy,
) *PullConsumer {
	p2 := policy.withDefaults()
	return &PullConsumer{
		client:   client,
		agentID:  agentID,
		pool:     p,
		executor: executor,
		reporter: reporter,
		backoff:  backoff.NewDynamic(p2.PullIntervalMin, p2.PullIntervalMax, p2.BackoffFactor),
		clock:    clock,
		log:      log.With(port.F("svc", "pull_consumer")),
		policy:   p2,
	}
}

// Run 启动消费循环，随 ctx 取消退出。
func (c *PullConsumer) Run(ctx context.Context) {
	c.log.Info("pull consumer started",
		port.F("interval_min", c.policy.PullIntervalMin.String()),
		port.F("interval_max", c.policy.PullIntervalMax.String()),
		port.F("max_concurrency", c.policy.MaxConcurrency))

	for {
		select {
		case <-ctx.Done():
			c.log.Info("pull consumer stopped")
			return
		default:
		}

		capacity := c.pool.Capacity()
		if capacity <= 0 {
			// 本地已满：不发起拉取，避免无意义请求打到网关。
			wait := c.backoff.Next(false, true)
			c.sleep(ctx, wait)
			continue
		}

		dispatches, err := c.client.Pull(ctx, string(c.agentID), capacity)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Warn("pull failed", port.F("err", err.Error()))
			c.sleep(ctx, c.backoff.Next(false, false))
			continue
		}

		gotWork := len(dispatches) > 0
		for _, d := range dispatches {
			c.dispatch(ctx, d)
		}
		c.sleep(ctx, c.backoff.Next(gotWork, false))
	}
}

// dispatch 把任务投递到本地协程池；池满则归还网关队列。
func (c *PullConsumer) dispatch(ctx context.Context, d port.TaskDispatch) {
	job := pool.Job{
		ID: d.TaskID,
		Fn: func(jobCtx context.Context) {
			if err := c.executor.Execute(jobCtx, d); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("task execute failed", port.F("task_id", d.TaskID), port.F("err", err.Error()))
			}
		},
	}
	if err := c.pool.Submit(job); err != nil {
		// 背压：本地队列已满，任务不消费，立即归还网关。
		c.log.Warn("local pool saturated, release task back to gateway",
			port.F("task_id", d.TaskID))
		if rerr := c.reporter.ReportRelease(ctx, d.TaskID, d.LockToken); rerr != nil {
			c.log.Error("report release failed", port.F("task_id", d.TaskID), port.F("err", rerr.Error()))
		}
	}
}

func (c *PullConsumer) sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
