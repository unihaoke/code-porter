package gateway

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// LifecycleConfig 生命周期守护配置。
type LifecycleConfig struct {
	// LockSweepInterval 任务锁扫描间隔。
	LockSweepInterval time.Duration
	// TTLSweepInterval 任务 TTL 扫描间隔。
	TTLSweepInterval time.Duration
	// HeartbeatSweepInterval Agent 心跳扫描间隔。
	HeartbeatSweepInterval time.Duration
	// RetentionInterval 终态任务清理间隔。
	RetentionInterval time.Duration
	// TaskRetention 终态任务保留时长，超过后从内存删除。
	TaskRetention time.Duration
}

func (c LifecycleConfig) withDefaults() LifecycleConfig {
	if c.LockSweepInterval <= 0 {
		c.LockSweepInterval = 5 * time.Second
	}
	if c.TTLSweepInterval <= 0 {
		c.TTLSweepInterval = 30 * time.Second
	}
	if c.HeartbeatSweepInterval <= 0 {
		c.HeartbeatSweepInterval = 10 * time.Second
	}
	if c.RetentionInterval <= 0 {
		c.RetentionInterval = 5 * time.Minute
	}
	if c.TaskRetention <= 0 {
		c.TaskRetention = 30 * time.Minute
	}
	return c
}

// TaskLifecycleService 网关侧的后台守护：任务锁回收、TTL 超时、死信裁决、心跳离线、终态清理。
//
// 这是 PRD「任务锁 / 重试 / 死信 / 超时控制」的执行者：
// 保证 Agent 宕机或本地卡死时任务不会永久停留在 running 状态。
type TaskLifecycleService struct {
	taskRepo  task.TaskRepository
	queueRepo task.TaskQueueRepository
	registry  *AgentRegistry
	broker    port.TaskEventBroker
	clock     port.Clock
	log       port.Logger
	policy    TaskPolicy
	cfg       LifecycleConfig
}

// NewTaskLifecycleService 构造守护服务。
func NewTaskLifecycleService(
	taskRepo task.TaskRepository,
	queueRepo task.TaskQueueRepository,
	registry *AgentRegistry,
	broker port.TaskEventBroker,
	clock port.Clock,
	log port.Logger,
	policy TaskPolicy,
	cfg LifecycleConfig,
) *TaskLifecycleService {
	return &TaskLifecycleService{
		taskRepo:  taskRepo,
		queueRepo: queueRepo,
		registry:  registry,
		broker:    broker,
		clock:     clock,
		log:       log.With(port.F("svc", "lifecycle")),
		policy:    policy.withDefaults(),
		cfg:       cfg.withDefaults(),
	}
}

// Start 启动守护协程，随 ctx 取消退出。
func (s *TaskLifecycleService) Start(ctx context.Context) {
	go s.loop(ctx, s.cfg.LockSweepInterval, s.sweepLocks)
	go s.loop(ctx, s.cfg.TTLSweepInterval, s.sweepTTL)
	go s.loop(ctx, s.cfg.HeartbeatSweepInterval, s.sweepHeartbeat)
	go s.loop(ctx, s.cfg.RetentionInterval, s.sweepRetention)
	s.log.Info("lifecycle service started",
		port.F("lock_sweep", s.cfg.LockSweepInterval.String()),
		port.F("ttl_sweep", s.cfg.TTLSweepInterval.String()),
		port.F("heartbeat_sweep", s.cfg.HeartbeatSweepInterval.String()))
}

func (s *TaskLifecycleService) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						s.log.Error("lifecycle sweep panic recovered", port.F("panic", r))
					}
				}()
				fn(ctx)
			}()
		}
	}
}

// sweepLocks 回收锁超时的 running 任务：可重试则回队列，否则进入死信。
func (s *TaskLifecycleService) sweepLocks(ctx context.Context) {
	tasks, err := s.taskRepo.FindByStatus(ctx, task.StatusRunning)
	if err != nil || len(tasks) == 0 {
		return
	}
	now := s.clock.Now()
	for _, t := range tasks {
		requeued, err := t.ExpireLock(now)
		if err != nil {
			continue
		}
		if !requeued && t.Status() != task.StatusRunning {
			// 已进入死信。
			if err := s.taskRepo.Save(ctx, t); err != nil {
				continue
			}
			_ = s.queueRepo.Remove(ctx, t.AgentID(), t.ID())
			_ = s.broker.Publish(ctx, port.TaskEvent{
				TaskID:  string(t.ID()),
				Type:    port.EventError,
				Code:    string(apperr.CodeDeadLetter),
				Message: t.ErrorMessage(),
				At:      now,
			})
			s.log.Warn("task moved to dead letter", port.F("task_id", string(t.ID())))
			continue
		}
		if requeued {
			if err := s.taskRepo.Save(ctx, t); err != nil {
				continue
			}
			if err := s.queueRepo.Requeue(ctx, t.AgentID(), t.ID()); err != nil {
				s.log.Error("requeue expired task failed", port.F("task_id", string(t.ID())), port.F("err", err.Error()))
				continue
			}
			s.log.Warn("task lock expired, requeued",
				port.F("task_id", string(t.ID())),
				port.F("attempt", t.Attempts()),
				port.F("max_retry", t.MaxRetry()))
		}
	}
}

// sweepTTL 处理 TTL 到期的任务：直接置为 timeout 终态并通知调用方。
func (s *TaskLifecycleService) sweepTTL(ctx context.Context) {
	tasks, err := s.taskRepo.FindByStatus(ctx, task.StatusPending)
	if err != nil || len(tasks) == 0 {
		return
	}
	now := s.clock.Now()
	for _, t := range tasks {
		if !t.ExpireTTL(now) {
			continue
		}
		if err := s.taskRepo.Save(ctx, t); err != nil {
			continue
		}
		_ = s.queueRepo.Remove(ctx, t.AgentID(), t.ID())
		_ = s.broker.Publish(ctx, port.TaskEvent{
			TaskID:  string(t.ID()),
			Type:    port.EventError,
			Code:    string(apperr.CodeTimeout),
			Message: t.ErrorMessage(),
			At:      now,
		})
		s.log.Warn("task ttl expired", port.F("task_id", string(t.ID())))
	}
}

// sweepHeartbeat 心跳超时 Agent 置离线。
func (s *TaskLifecycleService) sweepHeartbeat(ctx context.Context) {
	agents, err := s.registry.List(ctx)
	if err != nil {
		return
	}
	now := s.clock.Now()
	for _, a := range agents {
		if !a.SweepHeartbeat(now) {
			continue
		}
		if err := s.registry.repo.Save(ctx, a); err != nil {
			continue
		}
		s.log.Warn("agent heartbeat expired, marked offline", port.F("agent", string(a.ID())))
	}
}

// sweepRetention 清理过期的终态任务，避免内存无限增长。
func (s *TaskLifecycleService) sweepRetention(ctx context.Context) {
	tasks, err := s.taskRepo.FindByStatus(ctx,
		task.StatusSuccess, task.StatusFailed, task.StatusTimeout, task.StatusDeadLetter)
	if err != nil {
		return
	}
	now := s.clock.Now()
	for _, t := range tasks {
		if t.FinishedAt().IsZero() || now.Sub(t.FinishedAt()) < s.cfg.TaskRetention {
			continue
		}
		if err := s.taskRepo.Delete(ctx, t.ID()); err != nil {
			continue
		}
		s.broker.Unsubscribe(string(t.ID()))
	}
}
