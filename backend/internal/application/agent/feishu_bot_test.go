package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/pkg/pool"
)

// fakeIMRunner 记录回复内容。
type fakeIMRunner struct {
	mu     sync.Mutex
	texts  []string
	cards  []string
	failOn int
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
	f.cards = append(f.cards, markdown)
	return nil
}

func (f *fakeIMRunner) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...), append([]string(nil), f.cards...)
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

// TestLocalResultReporter 终态收敛与一次性关闭。
func TestLocalResultReporter(t *testing.T) {
	r := newLocalResultReporter()
	if err := r.ReportProgress(context.Background(), "t", "l", nil); err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.ReportSuccess(context.Background(), "t", "l", "答案") }()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ok || r.result != "答案" {
		t.Fatalf("result mismatch: ok=%v result=%q", r.ok, r.result)
	}
}

// TestFeishuBotOnMessageFilters 群聊未 @、空文本、重复事件被忽略，且不触发执行器。
func TestFeishuBotOnMessageFilters(t *testing.T) {
	runner := &fakeIMRunner{}
	execRunner := &recordingRunner{model: model.ClaudeCode}
	rep := &recordingReporter{}
	exec := NewTaskExecutor(&fakeMCPRegistry{runner: execRunner}, rep, nopLogger{}, Policy{})
	wp := pool.New(4, 8)
	wp.Start(context.Background())
	defer wp.Stop()

	svc := NewFeishuBotService(runner, exec, wp, FeishuBotConfig{
		Model:       model.ClaudeCode,
		MentionOnly: true,
		Ack:         false,
	}, Policy{}, nopLogger{})

	// 群聊未 @：忽略。
	if err := svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "g1", ChatID: "c", ChatType: port.ChatGroup, Text: "hi", Mentioned: false,
	}); err != nil {
		t.Fatal(err)
	}
	// 空文本：忽略。
	if err := svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "g2", ChatID: "c", ChatType: port.ChatP2P, Text: "  ",
	}); err != nil {
		t.Fatal(err)
	}
	// 重复事件：第二条忽略。
	msg := port.IMBotMessage{EventID: "p1", ChatID: "c", ChatType: port.ChatP2P, Text: "你好"}
	if err := svc.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	// 等待唯一一条被受理的消息执行完成（卡片回复）。
	deadline := time.After(3 * time.Second)
	for {
		_, cards := runner.snapshot()
		if len(cards) >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("p2p message was not executed and replied")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if execRunner.lastReq.Prompt != "你好" {
		t.Fatalf("executor prompt = %q", execRunner.lastReq.Prompt)
	}
	_, cards := runner.snapshot()
	if len(cards) != 1 {
		t.Fatalf("exactly one reply expected, got %d: %#v", len(cards), cards)
	}
}

// TestFeishuBotSystemPromptAndAck 系统提示拼入、ACK 先于结果发送。
func TestFeishuBotSystemPromptAndAck(t *testing.T) {
	runner := &fakeIMRunner{}
	execRunner := &recordingRunner{model: model.ClaudeCode}
	exec := NewTaskExecutor(&fakeMCPRegistry{runner: execRunner}, &recordingReporter{}, nopLogger{}, Policy{})
	wp := pool.New(2, 4)
	wp.Start(context.Background())
	defer wp.Stop()

	svc := NewFeishuBotService(runner, exec, wp, FeishuBotConfig{
		Model:        model.ClaudeCode,
		Ack:          true,
		SystemPrompt: "你是代码助手",
	}, Policy{}, nopLogger{})

	_ = svc.OnMessage(context.Background(), port.IMBotMessage{
		EventID: "x1", ChatID: "c", ChatType: port.ChatP2P, Text: "解释这个",
	})
	deadline := time.After(3 * time.Second)
	for {
		texts, cards := runner.snapshot()
		if len(texts) >= 1 && len(cards) >= 1 {
			if texts[0] == "" {
				t.Fatal("ack text empty")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("ack+result missing: texts=%v cards=%v", texts, cards)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := execRunner.lastReq.Prompt; got != "你是代码助手\n\n解释这个" {
		t.Fatalf("system prompt not prepended: %q", got)
	}
}
