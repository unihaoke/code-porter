package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// contextKey 上下文键类型。
type contextKey string

const (
	ctxKeyAPIKeyID contextKey = "api_key_id"
	ctxKeyAgent    contextKey = "agent"
)

// APIKeyAuthenticator 对外 API Key 鉴权。
type APIKeyAuthenticator struct {
	keys map[string]string // key -> keyID
}

// NewAPIKeyAuthenticator 构造鉴权器。
func NewAPIKeyAuthenticator(keys []string) *APIKeyAuthenticator {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		m[k] = shortHash(k)
	}
	return &APIKeyAuthenticator{keys: m}
}

// Authenticate 从请求中提取并校验 API Key，返回 keyID。
func (a *APIKeyAuthenticator) Authenticate(r *http.Request) (string, error) {
	if len(a.keys) == 0 {
		// 未配置任何 Key：MVP 单机自用场景放行，但显式标记 anonymous。
		return "anonymous", nil
	}
	key := extractAPIKey(r)
	if key == "" {
		return "", apperr.New(apperr.CodeUnauthorized, "missing api key")
	}
	id, ok := a.keys[key]
	if !ok {
		return "", apperr.New(apperr.CodeUnauthorized, "invalid api key")
	}
	return id, nil
}

func extractAPIKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
	}
	if v := r.Header.Get("X-API-Key"); v != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// WithAPIKeyAuth API Key 鉴权中间件。
func WithAPIKeyAuth(auth *APIKeyAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := auth.Authenticate(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyAPIKeyID, id)))
	}
}

// apiKeyIDFrom 取出上下文中的 API Key 标识。
func apiKeyIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyAPIKeyID).(string); ok {
		return v
	}
	return ""
}

// AgentTokenAuthenticator Agent 鉴权：LocalAgent 的所有出站请求必须携带 Agent Token。
type AgentTokenAuthenticator struct {
	findByToken func(ctx context.Context, token string) (*agent.Agent, error)
}

// NewAgentTokenAuthenticator 构造 Agent 鉴权器。
func NewAgentTokenAuthenticator(findByToken func(context.Context, string) (*agent.Agent, error)) *AgentTokenAuthenticator {
	return &AgentTokenAuthenticator{findByToken: findByToken}
}

// Authenticate 校验 Agent Token 并返回 Agent 聚合。
func (a *AgentTokenAuthenticator) Authenticate(r *http.Request) (*agent.Agent, error) {
	token := extractAgentToken(r)
	if token == "" {
		return nil, apperr.New(apperr.CodeUnauthorized, "missing agent token")
	}
	ag, err := a.findByToken(r.Context(), token)
	if err != nil {
		return nil, apperr.New(apperr.CodeUnauthorized, "invalid agent token")
	}
	return ag, nil
}

func extractAgentToken(r *http.Request) string {
	if v := r.Header.Get("X-Agent-Token"); v != "" {
		return strings.TrimSpace(v)
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		lower := strings.ToLower(auth)
		if strings.HasPrefix(lower, "agent-token ") {
			return strings.TrimSpace(auth[len("agent-token "):])
		}
		if strings.HasPrefix(lower, "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
	}
	return ""
}

// WithAgentAuth Agent 鉴权中间件。
func WithAgentAuth(auth *AgentTokenAuthenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ag, err := auth.Authenticate(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyAgent, ag)))
	}
}

// agentFrom 取出上下文中的 Agent。
func agentFrom(ctx context.Context) *agent.Agent {
	if v, ok := ctx.Value(ctxKeyAgent).(*agent.Agent); ok {
		return v
	}
	return nil
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
