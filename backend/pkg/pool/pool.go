// Package pool 提供固定容量、带背压能力的 goroutine 协程池。
//
// 语义（对应 PRD「本地协程池」）：
//   - maxConcurrency：硬上限，同时运行的任务数永不超过该值，保护本机 IDE；
//   - queueSize：本地内部等待队列长度；
//   - Submit 为非阻塞提交，队列满立即返回 ErrSaturated，调用方据此不再消费任务（背压）；
//   - 单个任务 panic 会被 recover，不会摧毁整个池。
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrSaturated 表示本地队列已满，调用方应停止消费并让任务停留在网关队列。
var ErrSaturated = errors.New("worker pool saturated")

// Job 池中执行单元。
type Job struct {
	// ID 便于日志追踪。
	ID string
	// Fn 实际执行逻辑，必须响应 ctx 取消。
	Fn func(ctx context.Context)
}

// PanicHandler 任务 panic 时的回调，可用于上报/告警。
type PanicHandler func(jobID string, recovered any)

// Pool 固定容量协程池。
type Pool struct {
	maxConcurrency int
	queueSize      int

	jobs      chan Job
	sem       chan struct{}
	quit      chan struct{}
	closeOnce sync.Once

	wg       sync.WaitGroup
	inflight atomic.Int32
	queued   atomic.Int32
	done     atomic.Int64

	onPanic PanicHandler
}

// Option 池配置。
type Option func(*Pool)

// WithPanicHandler 设置 panic 回调。
func WithPanicHandler(h PanicHandler) Option { return func(p *Pool) { p.onPanic = h } }

// New 创建协程池。maxConcurrency<=0 时回退为 1，queueSize<0 时回退为 0。
func New(maxConcurrency, queueSize int, opts ...Option) *Pool {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if queueSize < 0 {
		queueSize = 0
	}
	p := &Pool{
		maxConcurrency: maxConcurrency,
		queueSize:      queueSize,
		jobs:           make(chan Job, queueSize),
		sem:            make(chan struct{}, maxConcurrency),
		quit:           make(chan struct{}),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Start 启动 worker 协程，必须在使用前调用一次。
func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.maxConcurrency; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
}

func (p *Pool) worker(ctx context.Context, idx int) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.quit:
			return
		case job := <-p.jobs:
			p.queued.Add(-1)
			p.inflight.Add(1)
			p.safeRun(ctx, job)
			p.inflight.Add(-1)
			p.done.Add(1)
		}
	}
}

func (p *Pool) safeRun(ctx context.Context, job Job) {
	defer func() {
		if r := recover(); r != nil {
			if p.onPanic != nil {
				p.onPanic(job.ID, r)
			}
		}
	}()
	if job.Fn == nil {
		return
	}
	job.Fn(ctx)
}

// Submit 非阻塞提交任务；队列已满返回 ErrSaturated。
func (p *Pool) Submit(job Job) error {
	select {
	case <-p.quit:
		return fmt.Errorf("pool closed")
	default:
	}
	select {
	case p.jobs <- job:
		p.queued.Add(1)
		return nil
	default:
		return ErrSaturated
	}
}

// TrySubmit 在 ctx 未取消前尝试提交，队列满则等待一小段时间（用于弱背压场景）。
func (p *Pool) TrySubmit(ctx context.Context, job Job) error {
	select {
	case p.jobs <- job:
		p.queued.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.quit:
		return fmt.Errorf("pool closed")
	}
}

// Capacity 返回当前还能接收的任务数量（不含正在执行的部分）。
func (p *Pool) Capacity() int {
	free := p.queueSize - int(p.queued.Load())
	if free < 0 {
		return 0
	}
	return free
}

// Stats 运行时统计。
type Stats struct {
	MaxConcurrency int
	QueueSize      int
	Queued         int
	Inflight       int
	Done           int64
}

// Stats 返回快照。
func (p *Pool) Stats() Stats {
	return Stats{
		MaxConcurrency: p.maxConcurrency,
		QueueSize:      p.queueSize,
		Queued:         int(p.queued.Load()),
		Inflight:       int(p.inflight.Load()),
		Done:           p.done.Load(),
	}
}

// Stop 停止接收新任务并等待在途任务结束。
func (p *Pool) Stop() {
	p.closeOnce.Do(func() { close(p.quit) })
	p.wg.Wait()
}
