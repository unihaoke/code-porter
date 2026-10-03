package http

import (
	"net/http"
	"strings"

	"github.com/codeporter/code-porter/pkg/apperr"
)

// AdminAuthenticator 网页管理端鉴权器。
//
// 令牌优先级：Authorization: Bearer <token> → X-Admin-Token → 查询串 token（便于调试）。
type AdminAuthenticator struct {
	token string
}

// NewAdminAuthenticator 构造鉴权器。
func NewAdminAuthenticator(token string) *AdminAuthenticator {
	return &AdminAuthenticator{token: strings.TrimSpace(token)}
}

// Token 返回生效的令牌。
func (a *AdminAuthenticator) Token() string { return a.token }

// Enabled 是否启用了管理端鉴权（未配置令牌时所有请求放行，仅限本地调试）。
func (a *AdminAuthenticator) Enabled() bool { return a.token != "" }

// Authenticate 校验请求。
func (a *AdminAuthenticator) Authenticate(r *http.Request) error {
	if !a.Enabled() {
		return nil
	}
	if extractAdminToken(r) == a.token {
		return nil
	}
	return apperr.New(apperr.CodeUnauthorized, "invalid admin token")
}

// extractAdminToken 从请求中提取管理端令牌。
func extractAdminToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Admin-Token")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):])
	}
	return ""
}

// WithAdminAuth 管理端鉴权中间件。
func WithAdminAuth(auth *AdminAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := auth.Authenticate(r); err != nil {
			writeErr(w, err)
			return
		}
		next(w, r)
	}
}
