package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolRespectsConcurrencyLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var peak, running atomic.Int32
	p := New(2, 8)
	p.Start(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		if err := p.Submit(Job{ID: "j", Fn: func(context.Context) {
			defer wg.Done()
			cur := running.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
		}}); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("expected max 2 concurrent jobs, got %d", peak.Load())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestPoolBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	p := New(1, 1)
	p.Start(ctx)

	// 第一个任务占住唯一 worker（等待 worker 真正取走，避免竞态）。
	if err := p.Submit(Job{ID: "block", Fn: func(context.Context) { <-release }}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitFor(t, func() bool { return p.Stats().Inflight == 1 })

	// 第二个任务占住唯一队列位。
	if err := p.Submit(Job{ID: "queued", Fn: func(context.Context) {}}); err != nil {
		t.Fatalf("submit queued: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := p.Submit(Job{ID: "too-many", Fn: func(context.Context) {}}); !errors.Is(err, ErrSaturated) {
		t.Fatalf("expected ErrSaturated, got %v", err)
	}
	if p.Capacity() != 0 {
		t.Fatalf("expected zero capacity, got %d", p.Capacity())
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	if p.Stats().Done < 2 {
		t.Fatalf("expected at least 2 finished jobs, got %d", p.Stats().Done)
	}
}

func TestPoolRecoversPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var panicked atomic.Int32
	p := New(1, 4, WithPanicHandler(func(string, any) { panicked.Add(1) }))
	p.Start(ctx)

	done := make(chan struct{})
	if err := p.Submit(Job{ID: "boom", Fn: func(context.Context) { panic("boom") }}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := p.Submit(Job{ID: "ok", Fn: func(context.Context) { close(done) }}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not recover from panic")
	}
	if panicked.Load() != 1 {
		t.Fatalf("expected panic handler called once, got %d", panicked.Load())
	}
	p.Stop()
}
