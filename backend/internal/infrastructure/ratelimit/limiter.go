// Package ratelimit 提供网关侧限流：单 API Key QPS 令牌桶 + 全局在途请求上限。
package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/codeporter/code-porter/pkg/apperr"
)

// TokenBucket 令牌桶限流器（不可跨 key 共享）。
type TokenBucket struct {
	mu       sync.Mutex
	rate     float64 // 每秒生成令牌数
	burst    float64 // 桶容量
	tokens   float64
	lastTime time.Time
}

// NewTokenBucket 构造令牌桶；rate<=0 表示不限流。
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	if burst <= 0 {
		burst = 1
	}
	return &TokenBucket{
		rate:     rate,
		burst:    float64(burst),
		tokens:   float64(burst),
		lastTime: time.Now(),
	}
}

// Allow 尝试取一个令牌。
func (b *TokenBucket) Allow() bool {
	if b.rate <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastTime).Seconds()
	if elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
		b.lastTime = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// KeyedLimiter 按 Key 维度的限流器集合（每个 API Key 一个桶）。
type KeyedLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	buckets map[string]*TokenBucket
	// maxKeys 防止恶意海量 key 撑爆内存。
	maxKeys int
}

// NewKeyedLimiter 构造限流器。
func NewKeyedLimiter(rate float64, burst, maxKeys int) *KeyedLimiter {
	if maxKeys <= 0 {
		maxKeys = 10000
	}
	return &KeyedLimiter{rate: rate, burst: burst, buckets: make(map[string]*TokenBucket), maxKeys: maxKeys}
}

// Allow 判定指定 key 是否放行。
func (l *KeyedLimiter) Allow(key string) bool {
	if l.rate <= 0 {
		return true
	}
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.mu.Unlock()
			return false
		}
		b = NewTokenBucket(l.rate, l.burst)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.Allow()
}

// GlobalLimiter 全局在途请求上限（保护网关与本地 PC 不被打爆）。
type GlobalLimiter struct {
	mu      sync.Mutex
	max     int
	current int
	cond    *sync.Cond
}

// NewGlobalLimiter 构造全局限流器；max<=0 表示不限制。
func NewGlobalLimiter(max int) *GlobalLimiter {
	g := &GlobalLimiter{max: max}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Acquire 获取一个在途名额。
func (g *GlobalLimiter) Acquire(ctx context.Context) error {
	if g.max <= 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.current >= g.max {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 等待释放；使用带超时的等待避免永久挂起。
		done := make(chan struct{})
		go func() {
			g.cond.Wait()
			close(done)
		}()
		g.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			g.mu.Lock()
			return ctx.Err()
		}
		g.mu.Lock()
	}
	g.current++
	return nil
}

// Release 释放名额。
func (g *GlobalLimiter) Release() {
	if g.max <= 0 {
		return
	}
	g.mu.Lock()
	g.current--
	if g.current < 0 {
		g.current = 0
	}
	g.mu.Unlock()
	g.cond.Broadcast()
}

// Inflight 当前在途数量。
func (g *GlobalLimiter) Inflight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}

// ErrLimited 统一限流错误。
var ErrLimited = apperr.New(apperr.CodeRateLimited, "too many requests, please retry later")
