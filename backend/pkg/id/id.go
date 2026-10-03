// Package id 提供轻量、无依赖的全局唯一 ID 生成能力。
//
// 格式：<unix毫秒base36>-<随机base36>-<原子序号>，既可读又避免单点依赖。
package id

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

var sequence uint64

// New 生成一个新的 ID，prefix 用于区分业务语义（如 task_ / agent_ / chunk_）。
func New(prefix string) string {
	return prefix + encode(uint64(time.Now().UnixMilli())) + encode(randomUint32()) + encode(atomic.AddUint64(&sequence, 1))
}

// UUID 生成标准 UUID v4（依赖 google/uuid 的等价实现，这里用 crypto/rand 直接构造）。
func UUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极端情况退化为时间戳 + 序号，保证不 panic。
		return New("f")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func encode(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 12)
	for v > 0 {
		buf = append(buf, alphabet[v%36])
		v /= 36
	}
	// 反转
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func randomUint32() uint64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<32))
	if err != nil {
		return uint64(time.Now().UnixNano())
	}
	return n.Uint64()
}
