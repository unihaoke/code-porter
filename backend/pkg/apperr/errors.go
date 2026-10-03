// Package apperr 定义贯穿全系统的统一错误码与错误封装。
//
// 设计约定：
//  1. 领域层只返回领域错误（Code 固定为领域语义），不感知 HTTP；
//  2. 接口层（infrastructure/transport）负责把 Code 映射为 HTTP 状态码；
//  3. 所有跨层错误统一携带 Code，便于日志与排查。
package apperr

import (
	"errors"
	"fmt"
)

// Code 错误码。
type Code string

const (
	CodeInternal     Code = "internal_error"
	CodeInvalidParam Code = "invalid_param"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeRateLimited  Code = "rate_limited"
	CodeQueueFull    Code = "queue_full"
	CodeTimeout      Code = "timeout"
	CodeUnavailable  Code = "unavailable"
	CodeDeadLetter   Code = "dead_letter"
	CodeMCPFailure   Code = "mcp_failure"
	CodeNotConnected Code = "not_connected"
	CodeCanceled     Code = "canceled"
)

// Error 统一错误类型。
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

// New 构造错误。
func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Wrap 包装底层错误。
func Wrap(code Code, msg string, cause error) *Error {
	return &Error{Code: code, Message: msg, Cause: cause}
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
}

// Unwrap 支持 errors.Is / errors.As。
func (e *Error) Unwrap() error { return e.Cause }

// Is 判定 errors.Is：只要 Code 相同即认为同一类错误。
func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// CodeOf 从任意 error 中提取 Code，非 apperr.Error 返回 CodeInternal。
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// MessageOf 提取可读消息。
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// 预定义常用错误实例，方便领域层直接返回。
var (
	ErrInvalidParam = New(CodeInvalidParam, "invalid parameter")
	ErrUnauthorized = New(CodeUnauthorized, "unauthorized")
	ErrNotFound     = New(CodeNotFound, "resource not found")
	ErrTimeout      = New(CodeTimeout, "operation timeout")
	ErrUnavailable  = New(CodeUnavailable, "service unavailable")
	ErrQueueFull    = New(CodeQueueFull, "agent task queue is full")
	RateLimited     = New(CodeRateLimited, "too many requests")
)
