package user

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/pkg/apperr"
)

// 会话相关错误。
var (
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = apperr.New(apperr.CodeNotFound, "session not found")
	// ErrSessionEmptyToken 会话令牌摘要为空。
	ErrSessionEmptyToken = apperr.New(apperr.CodeInvalidParam, "session token hash is required")
	// ErrSessionEmptyUser 会话缺少归属用户。
	ErrSessionEmptyUser = apperr.New(apperr.CodeInvalidParam, "session user is required")
)

// Session 登录会话实体。服务端只存令牌的 SHA-256 哈希，可随时删除（登出/改密/禁用）。
type Session struct {
	tokenHash  string
	userID     ID
	createdAt  time.Time
	expiresAt  time.Time
	lastSeenAt time.Time
}

// SessionSpec 创建会话输入。
type SessionSpec struct {
	TokenHash string
	UserID    ID
	Now       time.Time
	TTL       time.Duration
}

// NewSession 创建会话（过期时间 = now + ttl）。
func NewSession(spec SessionSpec) (*Session, error) {
	if spec.TokenHash == "" {
		return nil, ErrSessionEmptyToken
	}
	if spec.UserID == "" {
		return nil, ErrSessionEmptyUser
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	ttl := spec.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Session{
		tokenHash:  spec.TokenHash,
		userID:     spec.UserID,
		createdAt:  now,
		expiresAt:  now.Add(ttl),
		lastSeenAt: now,
	}, nil
}

// Clone 返回副本。
func (s *Session) Clone() *Session {
	cp := *s
	return &cp
}

// TokenHash 令牌摘要。
func (s *Session) TokenHash() string { return s.tokenHash }

// UserID 所属用户。
func (s *Session) UserID() ID { return s.userID }

// CreatedAt 创建时间。
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// ExpiresAt 过期时间。
func (s *Session) ExpiresAt() time.Time { return s.expiresAt }

// LastSeenAt 最近访问时间。
func (s *Session) LastSeenAt() time.Time { return s.lastSeenAt }

// IsExpired 在给定时刻是否已过期。
func (s *Session) IsExpired(now time.Time) bool { return !now.Before(s.expiresAt) }

// Touch 刷新最近访问时间（不滑动续期，仅记录）。
func (s *Session) Touch(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	s.lastSeenAt = now
}

// RewriteSession 仓储反序列化回填，仅供仓储使用。
func RewriteSession(tokenHash string, userID ID, createdAt, expiresAt, lastSeenAt time.Time) *Session {
	return &Session{
		tokenHash:  tokenHash,
		userID:     userID,
		createdAt:  createdAt,
		expiresAt:  expiresAt,
		lastSeenAt: lastSeenAt,
	}
}

// SessionRepository 会话仓储端口。
type SessionRepository interface {
	// Save 创建或更新会话。
	Save(ctx context.Context, s *Session) error
	// FindByTokenHash 查询会话，不存在返回 ErrSessionNotFound。
	FindByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	// Delete 删除指定会话（登出）。
	Delete(ctx context.Context, tokenHash string) error
	// DeleteByUserExcept 删除某用户的全部会话，可保留一个当前会话（exceptHash 为空则全删）。
	DeleteByUserExcept(ctx context.Context, userID ID, exceptHash string) error
	// DeleteExpired 清理已过期会话（惰性/定期均可调用）。
	DeleteExpired(ctx context.Context, now time.Time) error
}
