// Package logging 提供 port.Logger 的轻量实现（基于标准库 log，零第三方依赖）。
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
)

// Level 日志级别。
type Level int

const (
	// LevelDebug 调试。
	LevelDebug Level = iota
	// LevelInfo 信息。
	LevelInfo
	// LevelWarn 警告。
	LevelWarn
	// LevelError 错误。
	LevelError
)

// ParseLevel 解析级别字符串。
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	}
	return LevelInfo
}

// String 级别名。
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "INFO"
}

// Logger 结构化日志实现，满足 port.Logger。
type Logger struct {
	mu     sync.Mutex
	out    io.Writer
	base   *log.Logger
	level  Level
	fields []port.Field
}

// New 创建日志器，w 为 nil 时使用 os.Stdout。
func New(w io.Writer, level Level) *Logger {
	if w == nil {
		w = os.Stdout
	}
	return &Logger{out: w, base: log.New(w, "", 0), level: level}
}

// With 派生携带固定字段的子日志器。
func (l *Logger) With(fields ...port.Field) port.Logger {
	if len(fields) == 0 {
		return l
	}
	merged := make([]port.Field, 0, len(l.fields)+len(fields))
	merged = append(merged, l.fields...)
	merged = append(merged, fields...)
	return &Logger{out: l.out, base: l.base, level: l.level, fields: merged}
}

func (l *Logger) log(level Level, msg string, fields ...port.Field) {
	if level < l.level {
		return
	}
	all := make([]port.Field, 0, len(l.fields)+len(fields))
	all = append(all, l.fields...)
	all = append(all, fields...)

	var sb strings.Builder
	sb.WriteString(time.Now().Format("2006-01-02T15:04:05.000Z07:00"))
	sb.WriteString(" ")
	sb.WriteString(level.String())
	sb.WriteString(" ")
	sb.WriteString(msg)
	for _, f := range all {
		sb.WriteString(" ")
		sb.WriteString(f.Key)
		sb.WriteString("=")
		sb.WriteString(formatValue(f.Value))
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.base.Output(2, sb.String())
}

// Debug 调试日志。
func (l *Logger) Debug(msg string, fields ...port.Field) { l.log(LevelDebug, msg, fields...) }

// Info 信息日志。
func (l *Logger) Info(msg string, fields ...port.Field) { l.log(LevelInfo, msg, fields...) }

// Warn 警告日志。
func (l *Logger) Warn(msg string, fields ...port.Field) { l.log(LevelWarn, msg, fields...) }

// Error 错误日志。
func (l *Logger) Error(msg string, fields ...port.Field) { l.log(LevelError, msg, fields...) }

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return `""`
		}
		if strings.ContainsAny(t, " \t\"\\") {
			return `"` + strings.ReplaceAll(t, `"`, `\"`) + `"`
		}
		return t
	case error:
		return `"` + t.Error() + `"`
	case nil:
		return "<nil>"
	default:
		return fmt.Sprintf("%v", v)
	}
}
