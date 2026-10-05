package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/pkg/pool"
)

// fakeIMRunner 记录流式卡片的创建/更新历史，模拟飞书单卡片实时更新。
type fakeIMRunner struct {
	mu            sync.Mutex
	texts         []string
	fallbackCards []string                      // SendCard 一次性兜底卡片
	updates       map[string][]port.IMCardState // messageID → 历次渲染状态
	nextID        int
}

func (f *fakeIMRunner) Start(context.Context) error { return nil }

func (f *fakeIMRunner) SendText(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return nil
}

func (f *fakeIMRunner) SendCard(_ context.Context, _, _, markdown string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallbackCards = append(f.fallbackCards, markdown)
	return nil
}

func (f *fakeIMRunner) OpenStreamCard(_ context.Context, _ string, state port.IMCardState) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := "om_card_" + string(rune('0'+f.nextID))
	if f.updates == nil {
		f.updates = map[string][]port.IMCardState{}
	}
	f.updates[id] = []port.IMCardState{state}
	return id, nil
}

func (f *fakeIMRunner) UpdateStreamCard(_ context.Context, messageID string, state port.IMCardState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updates == nil {
		f.updates = map[string][]port.IMCardState{}
	}
	f.updates[messageID] = append(f.updates[messageID], state)
	return nil
}

func (f *fakeIMRunner) lastState(id string) port.IMCardState {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.updates[id]
	return list[len(list)-1]
}

// waitForFinal 轮询直到某卡片进入终态（完成/失败）或超时。
func (f *fakeIMRunner) waitForFinal(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for id, list := range f.updates {
			last := list[len(list)-1]
			if last.Phase == port.IMCardDone || last.Phase == port.IMCardFailed {
				f.mu.Unlock()
				return id
			}
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("streaming card never reached final state")
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if len(x) > 0 && strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// TestIMSeenDeduper 重推事件只受理一次。
func TestIMSeenDeduper(t *testing.T) {
	d := newIMSeenDeduper(time.Minute)
	if !d.allow("e1") || !d.allow("e2") {
		t.Fatal("first sighting must pass")
	}
	if d.allow("e1") {
		t.Fatal("duplicate must be blocked")
	}
	if !d.allow("") {
		t.Fatal("empty id must pass (no dedup basis)")
	}
}

// TestFeishuBotOnMessageFilters 群聊未 @、空文本、重复事件被忽略。
func TestFeishuBotOnMessageFilters(t *testing.T) {
	runner := &fakeIMRunner{}
	execRunner := &recordingRunner{model: model.ClaudeCode}
	exec := NewTaskExecutor(&fakeMCPRegistry{runner: execRunner}, &recordingReporter{}, nopLogger{}, Policy{})
	wp := pool.New(4, 8)
	wp.Start(context.Background())
	defer wp.Stop()

	svc := NewFeishuBotService(runner, exec, wp, FeishuBotConfig{
		Model: model.ClaudeCode, MentionOnly: true,
	}, Policy{}, nopLogger{})

	if err := svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "g1", ChatID: "c", ChatType: port.ChatGroup, Text: "hi", Mentioned: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "g2", ChatID: "c", ChatType: port.ChatP2P, Text: "  ",
	}); err != nil {
		t.Fatal(err)
	}
	msg := port.IMBotMessage{EventID: "p1", ChatID: "c", ChatType: port.ChatP2P, Text: "你好"}
	if err := svc.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	id := runner.waitForFinal(t)
	if execRunner.lastReq.Prompt != "你好" {
		t.Fatalf("executor prompt = %q", execRunner.lastReq.Prompt)
	}
	// 每条输入只创建一张卡片（回复 2 次问题的关键保证）。
	if len(runner.updates) != 1 {
		t.Fatalf("exactly one card must be opened per message, got %d", len(runner.updates))
	}
	got := runner.lastState(id)
	if got.Phase != port.IMCardDone || got.Title != "CodePorter" || got.Body != "ok" {
		t.Fatalf("final card wrong: phase=%s title=%q body=%q", got.Phase, got.Title, got.Body)
	}
	if got.Summary != "已完成" || got.Footer != "" {
		t.Fatalf("finished card should be finalized: summary=%q footer=%q", got.Summary, got.Footer)
	}
}

// TestFeishuBotStreamingCard 流式片段会推进卡片更新，终态包含完整结果。
func TestFeishuBotStreamingCard(t *testing.T) {
	runner := &fakeIMRunner{}
	// recordingRunner 每个任务回 1 个片段 "ok"，至少能看到初始→终态两次更新。
	execRunner := &recordingRunner{model: model.ClaudeCode}
	exec := NewTaskExecutor(&fakeMCPRegistry{runner: execRunner}, &recordingReporter{}, nopLogger{}, Policy{})
	wp := pool.New(2, 4)
	wp.Start(context.Background())
	defer wp.Stop()

	svc := NewFeishuBotService(runner, exec, wp, FeishuBotConfig{
		Model: model.ClaudeCode, SystemPrompt: "你是代码助手",
	}, Policy{}, nopLogger{})

	_ = svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "x1", ChatID: "c", ChatType: port.ChatP2P, Text: "解释这个",
	})
	id := runner.waitForFinal(t)

	// 卡片建卡即回执：任何情况下都不再发送独立的「处理中」文本。
	if len(runner.texts) != 0 {
		t.Fatalf("no standalone ack text expected, got %d: %v", len(runner.texts), runner.texts)
	}
	if execRunner.lastReq.Prompt != "你是代码助手\n\n解释这个" {
		t.Fatalf("system prompt not prepended: %q", execRunner.lastReq.Prompt)
	}
	if len(runner.updates[id]) < 2 {
		t.Fatalf("card should be updated from initial to final, got %d versions", len(runner.updates[id]))
	}
}

// TestCardStreamPartition 思考/工具过程与正文分流，footer 随阶段切换。
func TestCardStreamPartition(t *testing.T) {
	c := newCardStream(&fakeIMRunner{}, "c", "om_test", nopLogger{})

	// 初始：思考阶段，面板展开。
	c.append(port.ChunkThinking, "先分析需求")
	st := c.snapshot()
	if !containsAny(st.Process, "先分析需求") || st.Body != "" {
		t.Fatalf("thinking should land in process: %+v", st)
	}
	if !st.ProcessActive || st.Footer != "🧠 _正在思考…_" || st.Summary != "思考中" {
		t.Fatalf("thinking stage state wrong: active=%v footer=%q summary=%q", st.ProcessActive, st.Footer, st.Summary)
	}

	// 工具调用行进入过程区，footer 切换为工具态。
	c.append(port.ChunkTool, "🔧 Read {\"file_path\":\"x.go\"}")
	c.append(port.ChunkTool, "✅ Read 执行完成")
	st = c.snapshot()
	if !containsAny(st.Process, "🔧 Read") || !containsAny(st.Process, "✅ Read 执行完成") {
		t.Fatalf("tool lines missing in process: %q", st.Process)
	}
	if st.Footer != "🧰 _正在调用工具…_" {
		t.Fatalf("tool stage footer = %q", st.Footer)
	}

	// 正文开始：面板折叠、footer 切换为输出态。
	c.append(port.ChunkText, "答案是 ")
	c.append(port.ChunkText, "42")
	st = c.snapshot()
	if st.Body != "答案是 42" || st.ProcessActive {
		t.Fatalf("body stage wrong: body=%q active=%v", st.Body, st.ProcessActive)
	}
	if st.Footer != "✍️ _正在输出…_" || st.Summary != "正在输出" {
		t.Fatalf("body stage footer/summary wrong: %q %q", st.Footer, st.Summary)
	}
	if !containsAny(st.Process, "先分析需求") {
		t.Fatalf("process must be preserved while body streams: %q", st.Process)
	}

	// 终态：footer 清空、summary 定型。
	c.finishWith(port.IMCardDone, "答案是 42")
	st = c.snapshot()
	if st.Phase != port.IMCardDone || st.Footer != "" || st.Summary != "已完成" {
		t.Fatalf("done state wrong: %+v", st)
	}
}

// TestCardStreamFailure 失败态以错误文案收口。
func TestCardStreamFailure(t *testing.T) {
	c := newCardStream(&fakeIMRunner{}, "c", "om_test2", nopLogger{})
	c.append(port.ChunkThinking, "尝试中")
	c.finishWith(port.IMCardFailed, "❌ boom")
	st := c.snapshot()
	if st.Phase != port.IMCardFailed || st.Body != "❌ boom" || st.Summary != "执行失败" {
		t.Fatalf("failed state wrong: %+v", st)
	}
	// 重复收口不 panic、不回退状态。
	c.finishWith(port.IMCardDone, "不应覆盖")
	if c.snapshot().Phase != port.IMCardFailed {
		t.Fatal("second finish must not override terminal phase")
	}
}

// TestCardStreamTruncateTail 正文超长截头保尾。
func TestCardStreamTruncateTail(t *testing.T) {
	long := make([]rune, cardBodyMaxRunes+100)
	for i := range long {
		long[i] = 'x'
	}
	got := truncateTail(string(long), cardBodyMaxRunes)
	if !containsAny(got, "已省略") {
		t.Fatalf("overlong body should be head-truncated: len=%d", len(got))
	}
	if r := []rune(got); !strings.HasPrefix(string(r[len(r)-5:]), "xxxxx") {
		t.Fatal("truncation must keep the tail")
	}
}
