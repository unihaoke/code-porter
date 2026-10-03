// Package backoff 提供 Pull 模式下的动态轮询退避算法。
package backoff

import "time"

// Dynamic 动态退避器：
//   - 拉到任务 → 立即重置为最小间隔（快速消费）；
//   - 连续空轮询 → 按指数拉长间隔，直到最大值（降低网关 QPS 压力）。
type Dynamic struct {
	min, max time.Duration
	factor   float64
	current  time.Duration
	emptyCnt int
}

// NewDynamic 创建动态退避器，min<=0 时取 200ms，max<min 时取 min。
func NewDynamic(minInterval, maxInterval time.Duration, factor float64) *Dynamic {
	if minInterval <= 0 {
		minInterval = 200 * time.Millisecond
	}
	if maxInterval < minInterval {
		maxInterval = minInterval
	}
	if factor < 1 {
		factor = 1.6
	}
	return &Dynamic{min: minInterval, max: maxInterval, factor: factor, current: minInterval}
}

// Next 返回下一次轮询应等待的时间；gotWork 表示本轮是否拉到任务；
// busy 表示本地协程池是否已满（此时也应放慢拉取，避免无意义请求）。
func (b *Dynamic) Next(gotWork, busy bool) time.Duration {
	switch {
	case busy:
		// 本地繁忙：退避到最大间隔，避免空转请求打满网关。
		b.emptyCnt++
		b.current = b.max
	case gotWork:
		b.emptyCnt = 0
		b.current = b.min
	default:
		b.emptyCnt++
		next := time.Duration(float64(b.current) * b.factor)
		if next < b.min {
			next = b.min
		}
		if next > b.max {
			next = b.max
		}
		b.current = next
	}
	return b.current
}

// Reset 重置为最小间隔。
func (b *Dynamic) Reset() {
	b.emptyCnt = 0
	b.current = b.min
}

// Current 当前间隔。
func (b *Dynamic) Current() time.Duration { return b.current }
