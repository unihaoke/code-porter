// Package http 是网关的接口层（Driving Adapter）：对外暴露 OpenAI 兼容接口与 Agent 内部接口。
package http

import (
	"encoding/json"
	"net/http"

	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/openai"
)

// statusOf 把领域错误码映射为 HTTP 状态码。
func statusOf(err error) int {
	switch apperr.CodeOf(err) {
	case apperr.CodeInvalidParam:
		return http.StatusBadRequest
	case apperr.CodeUnauthorized, apperr.CodeForbidden:
		return http.StatusUnauthorized
	case apperr.CodeNotFound:
		return http.StatusNotFound
	case apperr.CodeRateLimited, apperr.CodeQueueFull:
		return http.StatusTooManyRequests
	case apperr.CodeTimeout:
		return http.StatusGatewayTimeout
	case apperr.CodeUnavailable, apperr.CodeNotConnected:
		return http.StatusServiceUnavailable
	case apperr.CodeConflict, apperr.CodeDeadLetter:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 统一错误响应（OpenAI 风格）。
func writeErr(w http.ResponseWriter, err error) {
	code := apperr.CodeOf(err)
	writeJSON(w, statusOf(err), openai.ErrorResponse{
		Error: openai.ErrorDetail{
			Message: apperr.MessageOf(err),
			Type:    string(code),
			Code:    string(code),
		},
	})
}
