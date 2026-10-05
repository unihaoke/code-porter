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

// 默认流式节拍（runner 未实现 port.IMBotCardPacer 时使用）。
const (
	defaultCardFlushInterval = 700 * time.Millisecond
	defaultCardLifetime      = 15 * time.Minute
	// cardBodyMaxRunes 正文区保留的最大字符数，超长截头保尾。
	cardBodyMaxRunes = 6000
)

// cardStream 把 AI 的流式片段实时刷到一张 IM 卡片/流式消息上。
//
// 分两个区域：
//   - process（思考链 + 工具调用行）：飞书侧为折叠面板；企微侧折叠为灰色引用摘要；
//   - body（最终正文）：飞书按前缀增量打字机渲染；企微整条 Markdown 刷新。
//
// 用户体验：一条用户消息只对应一条机器人消息，先看到「思考中」，随后工具调用逐条
// 出现，答案持续增长，结束时过程折叠、状态定型（失败以错误态）。
// 更新做节流并串行化（同一消息的两次平台写入不得交叉，否则版本会错乱）。
//
// 节拍参数来自 runner 的 port.IMBotCardPacer（平台频控/流式寿命不同，
// 如飞书 700ms、企微 2s/10min），未实现该接口时用内置默认值。
type cardStream struct {
	runner         port.IMBotRunner
	target         port.IMReplyTarget
	messageID      string
	flushInterval  time.Duration
	maxLifetimeDur time.Duration
	log            port.Logger

	mu          sync.Mutex
	process     strings.Builder
	body        strings.Builder
	bodyStarted bool
	// toolStage 最近一段过程是否为工具调用（决定 footer 显示思考还是工具文案）。
	toolStage bool
	phase     port.IMCardPhase
	dirty     bool

	// flushMu 把所有平台写入串行化：ticker 定时刷新与 finish 的最终刷新可能撞车。
	flushMu sync.Mutex
	// finishOnce 防止成功回调与超时兜底罕见地双重收口（重复 close channel 会 panic）。
	finishOnce sync.Once
	// started 标记 start() 是否执行过，决定 finishWith 是否等待 ticker 协程退出。
	started bool

	stop chan struct{}
	done chan struct{}
}

func newCardStream(runner port.IMBotRunner, target port.IMReplyTarget, messageID string, log port.Logger) *cardStream {
	interval, lifetime := defaultCardFlushInterval, defaultCardLifetime
	if pacer, ok := runner.(port.IMBotCardPacer); ok {
		if d := pacer.CardFlushInterval(); d > 0 {
			interval = d
		}
		if d := pacer.MaxCardLifetime(); d > 0 {
			lifetime = d
		}
	}
	return &cardStream{
		runner: runner, target: target, messageID: messageID,
		flushInterval: interval, maxLifetimeDur: lifetime,
		log:   log.With(port.F("cmp", "card_stream")),
		phase: port.IMCardRunning,
		stop:  make(chan struct{}), done: make(chan struct{}),
	}
}

// maxLifetime 返回平台允许的流式消息最大寿命（供服务层裁剪任务等待超时）。
func (c *cardStream) maxLifetime() time.Duration { return c.maxLifetimeDur }

// start 启动节流刷新循环。
func (c *cardStream) start() {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(c.flushInterval)
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

	// 串行化：finish 的最终写入必须排在最后一个 ticker 写入之后。
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
	// 终态以完整正文为准（纯正文；思考/工具过程留在折叠/摘要区，不在正文里重复）。
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

// IMBotServiceConfig 单个 IM 渠道的本地机器人服务配置（平台无关）。
type IMBotServiceConfig struct {
	// Channel 渠道名（feishu/wecom…），仅用于日志与观测。
	Channel string
	// Model 处理消息使用的本地 AI 工具。
	Model model.Model
	// MentionOnly 群聊中仅响应 @机器人 的消息（私聊不受限）。
	MentionOnly bool
	// SystemPrompt 附加在每条消息前的系统提示（可选）。
	SystemPrompt string
}

// IMBotService 本地 IM 机器人：平台入站消息 → 本机 AI 执行 → 单条消息实时流式回复。
//
// 平台无关：只依赖 port.IMBotRunner 抽象（飞书卡片 / 企微流式消息等由各自 runner 渲染）。
// 与网关任务链路完全解耦：不鉴权、不入队、不上报，直接复用 TaskExecutor 与本地协程池，
// 因此即使网关不可达，IM 机器人仍可独立工作。
type IMBotService struct {
	channel  string
	runner   port.IMBotRunner
	executor *TaskExecutor
	pool     *pool.Pool
	policy   Policy
	cfg      IMBotServiceConfig
	log      port.Logger

	seen *imSeenDeduper
}

// NewIMBotService 构造服务。
func NewIMBotService(runner port.IMBotRunner, executor *TaskExecutor, workerPool *pool.Pool,
	cfg IMBotServiceConfig, policy Policy, log port.Logger) *IMBotService {
	channel := cfg.Channel
	if channel == "" {
		channel = "im"
	}
	return &IMBotService{
		channel:  channel,
		runner:   runner,
		executor: executor,
		pool:     workerPool,
		policy:   policy.withDefaults(),
		cfg:      cfg,
		log:      log.With(port.F("svc", "im_bot"), port.F("channel", channel)),
		seen:     newIMSeenDeduper(10 * time.Minute),
	}
}

// SetRunner 注入运行时（runner 与 service 互为依赖，构造后闭环）。
func (s *IMBotService) SetRunner(runner port.IMBotRunner) { s.runner = runner }

// OnMessage 入站事件回调。必须快速返回（平台回调有超时窗口）：受理判断同步完成，
// 执行与回复放到后台 goroutine；返回 error 会触发平台重投，仅在「应重试」时返回。
func (s *IMBotService) OnMessage(ctx context.Context, msg port.IMBotMessage) error {
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
	// 复制一份，脱离平台回调的短生命周期 ctx。
	go s.handle(msg)
	return nil
}

// handle 后台执行单条消息并以「单条消息实时更新」方式回复。
//
// 每条用户消息在会话里只会新增 1 条机器人消息：建卡即回执
// （瞬间出现「思考中」状态），随后思考过程与答案都在这张卡片/这条流式消息上实时更新，
// 因此不再发送任何独立的「已收到/处理中」文本。
func (s *IMBotService) handle(msg port.IMBotMessage) {
	target := port.IMReplyTarget{ChatID: msg.ChatID, ReplyToken: msg.ReplyToken}
	// 先建一条流式「处理中」消息，后续所有思考/输出都更新它，不再产生新消息。
	openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	messageID, err := s.runner.OpenStreamCard(openCtx, target, port.IMCardState{
		Phase:   port.IMCardRunning,
		Title:   "CodePorter",
		Summary: "思考中",
		Footer:  "🧠 _正在思考…_",
	})
	cancel()
	if err != nil {
		// 建流失败（多为发消息权限不足/缺少回调凭证）：退化为一条普通 markdown 错误提示。
		s.log.Error("open streaming card failed", port.F("err", err.Error()))
		s.sendFallback(target, "⚠️ 机器人无法在该会话发送消息，请检查应用权限与机器人配置。错误："+err.Error())
		return
	}
	card := newCardStream(s.runner, target, messageID, s.log)
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
	// 平台流式消息有寿命上限（如企微首帧起 10 分钟内必须 finish）。超时收口的
	// 终态帧本身还要再花一次写请求（出站写超时 12s），因此等待窗口必须短于
	// 寿命，而不是「寿命 + 余量」，否则终帧可能越过平台硬限被丢弃。
	const finishGrace = 20 * time.Second
	if life := card.maxLifetime(); life > finishGrace {
		if cap := life - finishGrace; cap < wait {
			wait = cap
		}
	}
	select {
	case <-rep.done:
		// 终态卡片已在 reporter 内完成最终更新。
	case <-time.After(wait):
		s.cardFail(card, "本地执行超时，未拿到结果")
	}
}

// cardFail 以错误态收口一张卡片。
func (s *IMBotService) cardFail(card *cardStream, text string) {
	card.finishWith(port.IMCardFailed, "❌ "+text)
}

// sendFallback 发送一次性 markdown 消息（建流失败等兜底场景）。
func (s *IMBotService) sendFallback(target port.IMReplyTarget, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := s.runner.SendCard(ctx, target, "CodePorter", content); err != nil {
		s.log.Error("fallback reply failed", port.F("err", err.Error()))
	}
}
