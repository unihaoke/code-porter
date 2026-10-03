package port

import "fmt"

// Field 结构化日志字段。
type Field struct {
	Key   string
	Value any
}

// F 构造日志字段。
func F(key string, value any) Field { return Field{Key: key, Value: value} }

// Logger 日志端口。基础设施层提供实现（如 zap / slog 适配器）。
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	// With 返回携带固定字段的子日志器。
	With(fields ...Field) Logger
}

// NopLogger 空实现，用于测试。
type NopLogger struct{}

// Debug 忽略。
func (NopLogger) Debug(string, ...Field) {}

// Info 忽略。
func (NopLogger) Info(string, ...Field) {}

// Warn 忽略。
func (NopLogger) Warn(string, ...Field) {}

// Error 忽略。
func (NopLogger) Error(string, ...Field) {}

// With 返回自身。
func (n NopLogger) With(...Field) Logger { return n }

// SprintFields 将字段格式化为 k=v 形式，供简易实现使用。
func SprintFields(fields []Field) string {
	if len(fields) == 0 {
		return ""
	}
	out := " "
	for i, f := range fields {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%v", f.Key, f.Value)
	}
	return out
}
