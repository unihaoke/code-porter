package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/pkg/id"
	"github.com/codeporter/code-porter/pkg/pool"
)

// localResultReporter 本地任务结果收集器：任务不经网关，结果直接在进程内汇聚。
type localResultReporter struct {
	done chan struct{}

	mu      sync.Mutex
	ok      bool
	result  string
	failure string
}

func newLocalResultReporter() *localResultReporter {
	return &localResultReporter{done: make(chan struct{})}
}

func (r *localResultReporter) ReportProgress(context.Context, string, string, []port.ChunkPayload) error {
	return nil // 本地飞书机器人等终态即可，不做流式回推
}

func (r *localResultReporter) ReportSuccess(_ context.Context, _, _, result string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ok, r.result = true, result
	close(r.done)
	return nil
}

func (r *localResultReporter) ReportFailure(_ context.Context, _, _, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ok, r.failure = false, errMsg
	close(r.done)
	return nil
}

func (r *localResultReporter) ReportRelease(context.Context, string, string) error { return nil }

// imSeenDeduper 近期消息 ID 去重（IM 平台超时会重推事件）。
type imSeenDeduper struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func newIMSeenDeduper(ttl time.Duration) *imSeenDeduper {
	return &imSeenDeduper{seen: make(map[string]time.Time), ttl: ttl}
}

func (d *imSeenDeduper) allow(key string) bool {
	if key == "" {
		return true
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, t := range d.seen {
		if now.Sub(t) > d.ttl {
			delete(d.seen, k)
		}
	}
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = now
	return true
}

// FeishuBotConfig 本地飞书机器人服务配置。
type FeishuBotConfig struct {
	Model        model.Model
	MentionOnly  bool
	Ack          bool
	SystemPrompt string
}

// FeishuBotService 本地飞书机器人：长连接消息 → 本机 AI 执行 → OpenAPI 回复。
//
// 与网关任务链路完全解耦：不鉴权、不入队、不上报，直接复用 TaskExecutor 与本地协程池，
// 因此即使网关不可达，飞书机器人仍可独立工作。
type FeishuBotService struct {
	runner   port.IMBotRunner
	executor *TaskExecutor
	pool     *pool.Pool
	policy   Policy
	cfg      FeishuBotConfig
	log      port.Logger

	seen *imSeenDeduper
}

// NewFeishuBotService 构造服务。
func NewFeishuBotService(runner port.IMBotRunner, executor *TaskExecutor, workerPool *pool.Pool,
	cfg FeishuBotConfig, policy Policy, log port.Logger) *FeishuBotService {
	return &FeishuBotService{
		runner:   runner,
		executor: executor,
		pool:     workerPool,
		policy:   policy.withDefaults(),
		cfg:      cfg,
		log:      log.With(port.F("svc", "feishu_bot")),
		seen:     newIMSeenDeduper(10 * time.Minute),
	}
}

// SetRunner 注入运行时（runner 与 service 互为依赖，构造后闭环）。
func (s *FeishuBotService) SetRunner(runner port.IMBotRunner) { s.runner = runner }

// OnMessage 长连接事件回调。必须快速返回（飞书 3s 限制）：受理判断同步完成，
// 执行与回复放到后台 goroutine；返回 error 会触发平台重投，仅在「应重试」时返回。
func (s *FeishuBotService) OnMessage(ctx context.Context, msg port.IMBotMessage) error {
	if s.cfg.MentionOnly && msg.ChatType == port.ChatGroup && !msg.Mentioned {
		return nil
	}
	if strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	if !s.seen.allow(msg.EventID) {
		s.log.Debug("duplicated im event ignored", port.F("event_id", msg.EventID))
		return nil
	}
	// 复制一份，脱离 SDK 回调的短生命周期 ctx。
	go s.handle(msg)
	return nil
}

// handle 后台执行单条消息并回复。
func (s *FeishuBotService) handle(msg port.IMBotMessage) {
	replyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if s.cfg.Ack {
		if err := s.runner.SendText(replyCtx, msg.ChatID, "已收到，正在本地处理中…"); err != nil {
			s.log.Warn("send ack failed", port.F("err", err.Error()))
		}
	}
	cancel()

	prompt := msg.Text
	if s.cfg.SystemPrompt != "" {
		prompt = s.cfg.SystemPrompt + "\n\n" + prompt
	}
	d := port.TaskDispatch{
		TaskID: id.New("local_"),
		Model:  s.cfg.Model.String(),
		Prompt: prompt,
	}

	rep := newLocalResultReporter()
	// 本地 IM 任务用独立上下文：不能挂在协程池的 worker ctx 上，
	// 否则 Agent 停止时在途任务会被立即掐断（交由 executor 的超时与 Stop 宽限管理）。
	job := pool.Job{
		ID: d.TaskID,
		Fn: func(_ context.Context) {
			if err := s.executor.ExecuteWith(context.Background(), d, rep); err != nil {
				s.log.Warn("local im task ended with error",
					port.F("event_id", msg.EventID), port.F("err", err.Error()))
			}
		},
	}
	if err := s.pool.Submit(job); err != nil {
		s.reply(msg.ChatID, "⚠️ 本地任务队列已满，请稍后再发。", "")
		return
	}

	// 等待终态：在 MCP 超时之外留足上报与余量。
	wait := s.policy.MCPTimeout + 30*time.Second
	if wait <= 0 || wait > 15*time.Minute {
		wait = 15 * time.Minute
	}
	var content, title string
	select {
	case <-rep.done:
		rep.mu.Lock()
		if rep.ok {
			title, content = "CodePorter", rep.result
		} else {
			content = "❌ " + rep.failure
		}
		rep.mu.Unlock()
	case <-time.After(wait):
		content = "⏱ 本地执行超时，未拿到结果"
	}
	s.reply(msg.ChatID, content, title)
}

// reply 回推结果，失败仅记录日志。
func (s *FeishuBotService) reply(chatID, content, title string) {
	if content == "" {
		content = "（本地 AI 未返回内容）"
	}
	if len([]rune(content)) > maxIMReplyRunes {
		runes := []rune(content)
		content = string(runes[:maxIMReplyRunes]) + "\n\n…（内容过长已截断）"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.runner.SendCard(ctx, chatID, title, content); err != nil {
		s.log.Error("reply to feishu failed", port.F("err", err.Error()))
	}
}

// maxIMReplyRunes IM 单条回复保守长度（飞书卡片文本上限充足，仍做保护性截断）。
const maxIMReplyRunes = 4000
