package http

import (
	"context"
	"net/http"
	"strings"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/gateway"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/apikey"
	userpkg "github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// contextKey 上下文键类型。
type contextKey string

const (
	ctxKeyUser  contextKey = "session_user"
	ctxKeyKeyID contextKey = "api_key_id"
	ctxKeyAgent contextKey = "agent"
)

// userFromContext 取出当前登录/鉴权用户。
func userFromContext(ctx context.Context) *userpkg.User {
	u, ok := ctx.Value(ctxKeyUser).(*userpkg.User)
	if !ok {
		return nil
	}
	return u
}

// apiKeyIDFromContext 取出通过秘钥鉴权的 Key ID（审计/限流用）。
func apiKeyIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyKeyID).(apikey.ID)
	return string(id)
}

// agentFrom 取出 Agent 实例。
func agentFrom(ctx context.Context) *agent.Agent {
	ag, _ := ctx.Value(ctxKeyAgent).(*agent.Agent)
	return ag
}

// bearerToken 提取 Authorization: Bearer 或 X-API-Key 中的凭据。
func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		lower := strings.ToLower(auth)
		switch {
		case strings.HasPrefix(lower, "bearer "):
			return strings.TrimSpace(auth[len("Bearer "):])
		case strings.HasPrefix(lower, "agent-token "):
			return strings.TrimSpace(auth[len("agent-token "):])
		}
	}
	if v := r.Header.Get("X-API-Key"); v != "" {
		return strings.TrimSpace(v)
	}
	// 本地 Agent 出站请求沿用 X-Agent-Token 头携带秘钥。
	if v := r.Header.Get("X-Agent-Token"); v != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// AgentIDHeader 指定 Agent 实例 ID（多租户下 Agent 自报实例身份）。
const AgentIDHeader = "X-Agent-ID"

// AgentNameHeader Agent 自报机器名（首次注册使用）。
const AgentNameHeader = "X-Agent-Name"

// extractAgentID 实例 ID：优先 X-Agent-ID 头，回落 pull 接口的 agentId 查询参数。
func extractAgentID(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(AgentIDHeader)); v != "" {
		return v
	}
	return strings.TrimSpace(r.URL.Query().Get("agentId"))
}

// --- 会话鉴权（网页控制台） ---

// SessionAuthenticator 校验会话令牌并注入登录用户。
type SessionAuthenticator struct{ svc *authsvc.Service }

// NewSessionAuthenticator 构造会话鉴权器。
func NewSessionAuthenticator(svc *authsvc.Service) *SessionAuthenticator {
	return &SessionAuthenticator{svc: svc}
}

// sessionToken 只接受 Authorization: Bearer 形态的会话令牌。
func sessionToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	return ""
}

// Authenticate 校验 Bearer 会话令牌。
func (a *SessionAuthenticator) Authenticate(r *http.Request) (*userpkg.User, error) {
	return a.svc.AuthenticateSession(r.Context(), sessionToken(r))
}

// WithSessionAuth 会话鉴权中间件。
func WithSessionAuth(auth *SessionAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := auth.Authenticate(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, u)
		next(w, r.WithContext(ctx))
	}
}

// WithRequireAdmin 要求登录用户具备 admin 角色（需在 WithSessionAuth 之后）。
func WithRequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r.Context())
		if u == nil || !u.IsAdmin() {
			writeErr(w, apperr.New(apperr.CodeForbidden, "admin only"))
			return
		}
		next(w, r)
	}
}

// --- 秘钥鉴权（OpenAI 接口与 Agent 接入） ---

// APIKeyAuthenticator 按 scope 校验秘钥，注入属主用户与 Key ID。
type APIKeyAuthenticator struct {
	svc   *authsvc.Service
	scope apikey.Scope
}

// NewAPIKeyAuth 构造指定 scope 的秘钥鉴权器。
func NewAPIKeyAuth(svc *authsvc.Service, scope apikey.Scope) *APIKeyAuthenticator {
	return &APIKeyAuthenticator{svc: svc, scope: scope}
}

// WithAPIKeyAuth 秘钥鉴权中间件。
func WithAPIKeyAuth(auth *APIKeyAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, k, err := auth.svc.AuthenticateAPISecret(r.Context(), bearerToken(r), auth.scope)
		if err != nil {
			writeErr(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, u)
		ctx = context.WithValue(ctx, ctxKeyKeyID, k.ID())
		next(w, r.WithContext(ctx))
	}
}

// AgentAuthenticator 本地 Agent 接入鉴权：秘钥（agent scope）+ 实例 ID 双因子，
// 通过后自动注册/解析实例并注入上下文。
type AgentAuthenticator struct {
	svc      *authsvc.Service
	registry *gateway.AgentRegistry
}

// NewAgentAuthenticator 构造 Agent 鉴权器。
func NewAgentAuthenticator(svc *authsvc.Service, registry *gateway.AgentRegistry) *AgentAuthenticator {
	return &AgentAuthenticator{svc: svc, registry: registry}
}

// WithAgentAuth Agent 鉴权中间件。
func WithAgentAuth(auth *AgentAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret := bearerToken(r)
		instanceID := extractAgentID(r)
		name := strings.TrimSpace(r.Header.Get(AgentNameHeader))

		owner, _, err := auth.svc.AuthenticateAPISecret(r.Context(), secret, apikey.ScopeAgent)
		if err != nil {
			writeErr(w, err)
			return
		}
		ag, err := auth.registry.AuthenticateAndRegister(r.Context(), owner.ID(), agent.ID(instanceID), name)
		if err != nil {
			writeErr(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, owner)
		ctx = context.WithValue(ctx, ctxKeyAgent, ag)
		next(w, r.WithContext(ctx))
	}
}
