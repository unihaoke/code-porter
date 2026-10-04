package http

import (
	"net"
	"net/http"
	"strings"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/infrastructure/ratelimit"
)

// AuthHandlers 登录/登出/当前用户接口。
type AuthHandlers struct {
	svc          *authsvc.Service
	loginLimiter *ratelimit.KeyedLimiter
	log          port.Logger
}

// NewAuthHandlers 构造处理器。loginLimiter 为 nil 时使用默认 1qps/突发 5 的 IP 限流。
func NewAuthHandlers(svc *authsvc.Service, loginLimiter *ratelimit.KeyedLimiter, log port.Logger) *AuthHandlers {
	if loginLimiter == nil {
		loginLimiter = ratelimit.NewKeyedLimiter(1, 5, 10000)
	}
	return &AuthHandlers{svc: svc, loginLimiter: loginLimiter, log: log.With(port.F("h", "auth"))}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login 账号密码登录，签发会话令牌。
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !h.loginLimiter.Allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]string{"code": "rate_limited", "message": "too many login attempts, try again later"},
		})
		return
	}
	var req loginRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.svc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Logout 登出并删除当前会话。
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	_ = h.svc.Logout(r.Context(), sessionToken(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Me 返回当前登录用户。
func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": h.svc.ViewUser(u)})
}

// clientIP 提取客户端 IP（限流键）；优先 X-Forwarded-For 首段。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
