// Package auth 承载多租户账号体系的应用用例：登录/登出、会话校验、
// 用户管理（admin）、秘钥管理，以及对外秘钥的鉴权（CredentialService）。
//
// 用例层只依赖领域端口与 application/port 的加密抽象，不感知 HTTP 与 MySQL。
package auth

import (
	"context"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// Deps 装配认证服务所需的出站端口。
type Deps struct {
	Users     user.Repository
	Keys      apikey.Repository
	Sessions  user.SessionRepository
	Hasher    port.PasswordHasher
	Secrets   port.SecretHasher
	Generator port.CredentialGenerator
	Clock     port.Clock
	Logger    port.Logger
	// SessionTTL 会话有效期（默认 168h）。
	SessionTTL time.Duration
	// KeyUseThrottle 秘钥 last_used_at 落库节流间隔（默认 60s）。
	KeyUseThrottle time.Duration
	// AfterUserDelete 用户删除成功后的回调（清理其实例身份与 WebSocket 等）。
	AfterUserDelete func(ctx context.Context, deletedID user.ID)
}

// Service 认证与账号/秘钥管理用例集合。
type Service struct {
	deps Deps
	// mu 保护 useTouch（last_used 节流表）。
	mu       sync.Mutex
	useTouch map[string]time.Time
}

// NewService 构造服务并补齐默认值。
func NewService(d Deps) *Service {
	if d.SessionTTL <= 0 {
		d.SessionTTL = 168 * time.Hour
	}
	if d.KeyUseThrottle <= 0 {
		d.KeyUseThrottle = 60 * time.Second
	}
	return &Service{deps: d, useTouch: make(map[string]time.Time)}
}

// UserView 用户对外视图（严禁携带密码哈希）。
type UserView struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoginResult 登录成功结果。
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      UserView  `json:"user"`
}

// KeyView 秘钥对外视图（不含明文与哈希）。
type KeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Permission string     `json:"permission"`
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Expired    bool       `json:"expired"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreatedKeyResult 创建秘钥结果；Secret 为明文秘钥，仅此一次返回。
type CreatedKeyResult struct {
	KeyView
	Secret string `json:"secret"`
}

func toUserView(u *user.User) UserView {
	return UserView{
		ID:        string(u.ID()),
		Username:  u.Username(),
		Role:      u.Role().String(),
		Status:    u.Status().String(),
		CreatedAt: u.CreatedAt(),
		UpdatedAt: u.UpdatedAt(),
	}
}

// ViewUser 导出用户视图（HTTP 层使用）。
func (s *Service) ViewUser(u *user.User) UserView { return toUserView(u) }

// HashSecret 供传输层把当前会话明文令牌转哈希（改密保留当前会话用）。
func (s *Service) HashSecret(raw string) string { return s.deps.Secrets.Hash(raw) }

func toKeyView(k *apikey.APIKey, now time.Time) KeyView {
	scopes := make([]string, 0, len(k.Scopes()))
	for _, s := range k.Scopes() {
		scopes = append(scopes, s.String())
	}
	v := KeyView{
		ID:         string(k.ID()),
		Name:       k.Name(),
		Scopes:     scopes,
		Permission: string(k.Permission()),
		Prefix:     k.Prefix(),
		Expired:    k.Expired(now),
		CreatedAt:  k.CreatedAt(),
	}
	if k.HasExpiry() {
		t := k.ExpiresAt()
		v.ExpiresAt = &t
	}
	if !k.LastUsedAt().IsZero() {
		t := k.LastUsedAt()
		v.LastUsedAt = &t
	}
	return v
}

// requireAdmin 断言操作者是管理员。
func requireAdmin(actor *user.User) error {
	if actor == nil || !actor.IsAdmin() {
		return errAdminOnly
	}
	return nil
}
