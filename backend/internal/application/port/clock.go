// Package port 定义应用层的出站端口（Driven Ports）。
//
// 端口由应用层声明、基础设施层实现（依赖倒置），
// 保证领域/应用层不依赖任何具体框架、协议与第三方库。
package port

import "time"

// Clock 时间端口，便于测试注入假时钟。
type Clock interface {
	// Now 当前时间。
	Now() time.Time
}

// RealClock 系统时钟实现。
type RealClock struct{}

// Now 返回系统当前时间。
func (RealClock) Now() time.Time { return time.Now() }
