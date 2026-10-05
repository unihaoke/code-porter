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

// cardStream 把 AI 的流式片段实时刷到一张 IM 卡片上。
//
// 分两个区域：
//   - process（思考链 + 工具调用行）：折叠面板，进行中自动展开、正文开始后折叠；
//   - body（最终正文）：固定元素，平台按前缀增量做「打字机」逐字渲染。
//
// 飞书侧用户体验：只收到一张卡片，先看到「🧠 思考中」，随后工具调用逐条出现，
// 答案以打字机效果增长，结束时面板折叠、头部变绿（失败变红）。
// 更新做节流并串行化（两次 PATCH 不得交叉，否则卡片版本会错乱）。
type cardStream struct {
	runner    port.IMBotRunner
	chatID    string
	messageID string
	log       port.Logger

	mu          sync.Mutex
	process     strings.Builder
	body        strings.Builder
	bodyStarted bool
	// toolStage 最近一段过程是否为工具调用（决定 footer 显示思考还是工具文案）。
	toolStage bool
	phase     port.IMCardPhase
	dirty     bool

	// flushMu 把所有 PATCH 串行化：ticker 定时刷新与 finish 的最终刷新可能撞车。
	flushMu sync.Mutex
	// finishOnce 防止成功回调与超时兜底罕见地双重收口（重复 close channel 会 panic）。
	finishOnce sync.Once
	// started 标记 start() 是否执行过，决定 finishWith 是否等待 ticker 协程退出。
	started bool

	stop chan struct{}
	done chan struct{}
}

const (
	// cardFlushInterval 卡片最小刷新间隔。
	// streaming_mode 期间全量更新不占常规 QPS 配额（硬限 50 次/秒），
	// 700ms 兼顾打字流畅度与请求安全边界。
	cardFlushInterval = 700 * time.Millisecond
	// cardBodyMaxRunes 正文区保留的最大字符数，超长截头保尾。
	cardBodyMaxRunes = 6000
)

func newCardStream(runner port.IMBotRunner, chatID, messageID string, log port.Logger) *cardStream {
	return &cardStream{
		runner: runner, chatID: chatID, messageID: messageID,
		log:   log.With(port.F("cmp", "card_stream")),
		phase: port.IMCardRunning,
		stop:  make(chan struct{}), done: make(chan struct{}),
	}
}

// start 启动节流刷新循环。
func (c *cardStream) start() {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(cardFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.flushIfDirty()
			}
		}
	}()
}

// append 按片段语义分流到过程区或正文区。
func (c *cardStream) append(kind port.ChunkKind, text string) {
	if text == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case kind.IsBody():
		c.body.WriteString(text)
		c.bodyStarted = true
	case kind == port.ChunkTool:
		c.appendProcessLine(text)
		c.toolStage = true
	default: // ChunkThinking
		c.appendProcessBlock(text)
		c.toolStage = false
	}
	c.dirty = true
}

// appendProcessBlock 追加一段思考块：块之间空行分隔。
func (c *cardStream) appendProcessBlock(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if c.process.Len() > 0 {
		c.process.WriteString("\n\n")
	}
	c.process.WriteString(s)
}

// appendProcessLine 追加一行工具过程（🔧/✅/❌ 开头）。
func (c *cardStream) appendProcessLine(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if c.process.Len() > 0 {
		c.process.WriteString("\n")
	}
	c.process.WriteString(s)
}

// finishWith 终止刷新循环并以终态做最后一次同步更新；bodyText 非空时覆盖正文。
// 重复调用以第一次为准（sync.Once），避免成功/超时双收口产生 panic 或状态回退。
func (c *cardStream) finishWith(phase port.IMCardPhase, bodyText string) {
	c.finishOnce.Do(func() {
		c.mu.Lock()
		c.phase = phase
		if bodyText != "" {
			c.body.Reset()
			c.body.WriteString(bodyText)
			c.bodyStarted = true
		}
		c.dirty = true
		started := c.started
		c.mu.Unlock()
		if c.stop != nil {
			close(c.stop)
		}
		// 只有 start() 过才有 ticker goroutine 需要等待（单元测试可直接构造不 start）。
		if started {
			<-c.done
		}
		c.flushIfDirty()
	})
}

func (c *cardStream) flushIfDirty() {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	state := c.snapshot()
	c.dirty = false
	c.mu.Unlock()

	// 串行化：finish 的最终 PATCH 必须排在最后一个 ticker PATCH 之后。
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := c.runner.UpdateStreamCard(ctx, c.messageID, state); err != nil {
		c.log.Warn("update streaming card failed", port.F("err", err.Error()))
	}
}

// snapshot 组装当前卡片状态。flushIfDirty 在持 c.mu 时调用；
// 测试可在无并发下直接调用。
func (c *cardStream) snapshot() port.IMCardState {
	running := c.phase == port.IMCardRunning
	state := port.IMCardState{
		Phase:         c.phase,
		Title:         "CodePorter",
		Process:       strings.TrimSpace(c.process.String()),
		ProcessActive: running && !c.bodyStarted,
		Body:          truncateTail(c.body.String(), cardBodyMaxRunes),
	}
	switch {
	case !running:
		if c.phase == port.IMCardDone {
			state.Summary = "已完成"
		} else {
			state.Summary = "执行失败"
		}
	case c.bodyStarted:
		state.Summary, state.Footer = "正在输出", "✍️ _正在输出…_"
	case c.toolStage:
		state.Summary, state.Footer = "正在调用工具", "🧰 _正在调用工具…_"
	default:
		state.Summary, state.Footer = "思考中", "🧠 _正在思考…_"
	}
	return state
}

// truncateTail 超长文本截头保尾（长输出只关注最新进展）。
func truncateTail(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return "…（前面的内容已省略）\n" + string(r[len(r)-max:])
	}
	return s
}

// streamingReporter 适配 TaskExecutor 的 ResultReporter：
// 进度片段按类型推到卡片分区，终态触发定型渲染。
type streamingReporter struct {
	card *cardStream
	done chan struct{}

	closeOnce sync.Once
	mu        sync.Mutex
	ok        bool
	result    string
	failure   string
}

func newStreamingReporter(card *cardStream) *streamingReporter {
	return &streamingReporter{card: card, done: make(chan struct{})}
}

func (r *streamingReporter) ReportProgress(_ context.Context, _, _ string, chunks []port.ChunkPayload) error {
	for _, c := range chunks {
		r.card.append(c.Kind, c.Content)
	}
	return nil
}

func (r *streamingReporter) ReportSuccess(_ context.Context, _, _, result string) error {
	r.mu.Lock()
	r.ok, r.result = true, result
	r.mu.Unlock()
	// 终态以完整正文为准（纯正文；思考/工具过程留在折叠面板，不在正文里重复）。
	r.card.finishWith(port.IMCardDone, strings.TrimSpace(result))
	r.closeOnce.Do(func() { close(r.done) })
	return nil
}

func (r *streamingReporter) ReportFailure(_ context.Context, _, _, errMsg string) error {
	r.mu.Lock()
	r.ok, r.failure = false, errMsg
	r.mu.Unlock()
	r.card.finishWith(port.IMCardFailed, errMsg)
	r.closeOnce.Do(func() { close(r.done) })
	return nil
}

func (r *streamingReporter) ReportRelease(context.Context, string, string) error { return nil }

// imSeenDeduper 近期消息 ID 去重（IM 平台超时会重推事件、长连接重连也可能重投）。
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
	SystemPrompt string
}

// FeishuBotService 本地飞书机器人：长连接消息 → 本机 AI 执行 → 单卡片实时回复。
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
		s.log.Info("duplicated im event ignored",
			port.F("message_id", msg.EventID), port.F("chat_id", msg.ChatID))
		return nil
	}
	// 复制一份，脱离 SDK 回调的短生命周期 ctx。
	go s.handle(msg)
	return nil
}

// handle 后台执行单条消息并以「单卡片实时更新」方式回复。
//
// 每条用户消息在群里只会新增 1 条机器人消息（卡片）：卡片建卡即回执
// （瞬间出现「思考中」状态），随后思考过程与答案都在这张卡片上实时更新，
// 因此不再发送任何独立的「已收到/处理中」文本。
func (s *FeishuBotService) handle(msg port.IMBotMessage) {
	// 先建一张流式「处理中」卡片，后续所有思考/输出都更新它，不再产生新消息。
	openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	messageID, err := s.runner.OpenStreamCard(openCtx, msg.ChatID, port.IMCardState{
		Phase:   port.IMCardRunning,
		Title:   "CodePorter",
		Summary: "思考中",
		Footer:  "🧠 _正在思考…_",
	})
	cancel()
	if err != nil {
		// 建卡失败（多为发消息权限不足）：无法承载流式输出，退化为一条普通文本错误提示。
		s.log.Error("open streaming card failed", port.F("err", err.Error()))
		s.sendFallback(msg.ChatID, "⚠️ 机器人无法在该会话发送卡片消息，请检查应用的 im:message 发消息权限。错误："+err.Error())
		return
	}
	card := newCardStream(s.runner, msg.ChatID, messageID, s.log)
	card.start()

	prompt := msg.Text
	if s.cfg.SystemPrompt != "" {
		prompt = s.cfg.SystemPrompt + "\n\n" + prompt
	}
	d := port.TaskDispatch{
		TaskID: id.New("local_"),
		Model:  s.cfg.Model.String(),
		Prompt: prompt,
	}
	rep := newStreamingReporter(card)

	// 本地 IM 任务用独立上下文：不挂在协程池 worker ctx 上，避免停止时被立即掐断。
	job := pool.Job{
		ID: d.TaskID,
		Fn: func(_ context.Context) {
			if execErr := s.executor.ExecuteWith(context.Background(), d, rep); execErr != nil {
				s.log.Warn("local im task ended with error",
					port.F("message_id", msg.EventID), port.F("err", execErr.Error()))
			}
		},
	}
	if err := s.pool.Submit(job); err != nil {
		s.cardFail(card, "本地任务队列已满，请稍后再发。")
		return
	}

	wait := s.policy.MCPTimeout + 30*time.Second
	if wait <= 0 || wait > 15*time.Minute {
		wait = 15 * time.Minute
	}
	select {
	case <-rep.done:
		// 终态卡片已在 reporter 内完成最终更新。
	case <-time.After(wait):
		s.cardFail(card, "本地执行超时，未拿到结果")
	}
}

// cardFail 以错误态收口一张卡片。
func (s *FeishuBotService) cardFail(card *cardStream, text string) {
	card.finishWith(port.IMCardFailed, "❌ "+text)
}

// sendFallback 发送一次性文本卡片（建卡失败等兜底场景）。
func (s *FeishuBotService) sendFallback(chatID, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := s.runner.SendCard(ctx, chatID, "CodePorter", content); err != nil {
		s.log.Error("fallback reply failed", port.F("err", err.Error()))
	}
}
