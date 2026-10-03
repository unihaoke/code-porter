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

// DirectConsumer SSE 直连模式的消费者：维持出站 WebSocket，实时接收推送任务。
//
// 特点（PRD 3.2）：任务不进队列、低延迟；连接断开时当前任务中断，
// 因此这里实现了指数退避重连 + 心跳保活（PRD 5.2-2）。
type DirectConsumer struct {
	factory  port.DirectSessionFactory
	agentID  agent.ID
	pool     *pool.Pool
	executor *TaskExecutor
	backoff  *backoff.Dynamic
	log      port.Logger
	policy   Policy
}

// NewDirectConsumer 构造直连消费者。
func NewDirectConsumer(
	factory port.DirectSessionFactory,
	agentID agent.ID,
	p *pool.Pool,
	executor *TaskExecutor,
	log port.Logger,
	policy Policy,
) *DirectConsumer {
	p2 := policy.withDefaults()
	return &DirectConsumer{
		factory:  factory,
		agentID:  agentID,
		pool:     p,
		executor: executor,
		backoff:  backoff.NewDynamic(p2.ReconnectMin, p2.ReconnectMax, 2.0),
		log:      log.With(port.F("svc", "direct_consumer")),
		policy:   p2,
	}
}

// Run 启动直连消费循环，随 ctx 取消退出。
func (c *DirectConsumer) Run(ctx context.Context) {
	c.log.Info("direct consumer started", port.F("agent", string(c.agentID)))
	for {
		if ctx.Err() != nil {
			c.log.Info("direct consumer stopped")
			return
		}
		session, err := c.factory.Dial(ctx, string(c.agentID))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := c.backoff.Next(false, false)
			c.log.Warn("dial gateway failed, retry later",
				port.F("err", err.Error()), port.F("wait", wait.String()))
			c.sleep(ctx, wait)
			continue
		}
		c.backoff.Reset()
		c.log.Info("direct session established", port.F("agent", string(c.agentID)))
		c.consume(ctx, session)
		_ = session.Close()
		c.log.Warn("direct session closed, reconnecting")
	}
}

func (c *DirectConsumer) consume(ctx context.Context, session port.DirectSession) {
	reporter := NewDirectReporter(session, string(c.agentID))
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-session.Errors():
			if !ok {
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("session error", port.F("err", err.Error()))
			}
			return
		case d, ok := <-session.Tasks():
			if !ok {
				return
			}
			c.submit(ctx, d, reporter)
		}
	}
}

func (c *DirectConsumer) submit(ctx context.Context, d port.TaskDispatch, reporter ResultReporter) {
	job := pool.Job{
		ID: d.TaskID,
		Fn: func(jobCtx context.Context) {
			if err := c.executor.ExecuteWith(jobCtx, d, reporter); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("direct task failed", port.F("task_id", d.TaskID), port.F("err", err.Error()))
			}
		},
	}
	if err := c.pool.Submit(job); err != nil {
		// 直连任务无法回退到队列，只能明确失败，让调用方快速感知。
		c.log.Warn("local pool saturated, reject direct task", port.F("task_id", d.TaskID))
		if rerr := reporter.ReportFailure(ctx, d.TaskID, d.LockToken, "local worker pool saturated"); rerr != nil {
			c.log.Error("report failure error", port.F("task_id", d.TaskID), port.F("err", rerr.Error()))
		}
	}
}

func (c *DirectConsumer) sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
