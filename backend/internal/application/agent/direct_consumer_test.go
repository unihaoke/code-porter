package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
)

// nopLogger 测试用空日志器（应用层测试不反向依赖基础设施层）。
type nopLogger struct{}

func (nopLogger) Debug(string, ...port.Field)      {}
func (nopLogger) Info(string, ...port.Field)       {}
func (nopLogger) Warn(string, ...port.Field)       {}
func (nopLogger) Error(string, ...port.Field)      {}
func (l nopLogger) With(...port.Field) port.Logger { return l }

type fakeSession struct {
	tasks chan port.TaskDispatch
	errs  chan error
	once  sync.Once
}

func (s *fakeSession) Tasks() <-chan port.TaskDispatch             { return s.tasks }
func (s *fakeSession) Send(context.Context, port.AckRequest) error { return nil }
func (s *fakeSession) Errors() <-chan error                        { return s.errs }
func (s *fakeSession) Close() error {
	s.once.Do(func() { close(s.errs) })
	return nil
}

type fakeFactory struct {
	dials atomic.Int64
}

func (f *fakeFactory) Dial(context.Context, string) (port.DirectSession, error) {
	f.dials.Add(1)
	// 会话一建立就立即结束，模拟服务端上下文错用 / 代理秒掐。
	errs := make(chan error, 1)
	errs <- errors.New("connection closed immediately")
	return &fakeSession{tasks: make(chan port.TaskDispatch), errs: errs}, nil
}

// TestDirectConsumerBacksOffOnShortLivedSessions 验证短连接不会引发重连风暴：
// 修复前会话秒断后无等待立即重拨，500ms 内会产生成千上万次拨号；
// 修复后必须按指数退避（20ms → 40ms → 80ms → 160ms 封顶）。
func TestDirectConsumerBacksOffOnShortLivedSessions(t *testing.T) {
	factory := &fakeFactory{}
	c := NewDirectConsumer(factory, agent.ID("agt_backoff"), nil, nil, nopLogger{}, Policy{
		ReconnectMin: 20 * time.Millisecond,
		ReconnectMax: 160 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done

	n := factory.dials.Load()
	if n < 3 {
		t.Fatalf("consumer should keep retrying, only %d dials", n)
	}
	if n > 8 {
		t.Fatalf("short-lived sessions not backed off: %d dials in 500ms (reconnect storm)", n)
	}
}
