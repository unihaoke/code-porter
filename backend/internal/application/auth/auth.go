package auth

import (
	"context"

	"github.com/codeporter/code-porter/internal/domain/user"
)

// dummyBcryptHash 用于在用户不存在时也执行一次等价开销的比较，拉平登录耗时侧信道。
const dummyBcryptHash = "$2a$10$0000000000000000000000uP9p0p0p0p0p0p0p0p0p0p0p0p0p0p0"

// Login 校验用户名密码并签发会话。
// 无论「用户不存在 / 密码错误 / 账号停用（密码阶段）」都返回相同错误。
func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	now := s.deps.Clock.Now()
	u, err := s.deps.Users.FindByUsername(ctx, username)
	if err != nil {
		// 消耗一次 bcrypt 比较时间，避免通过响应耗时判断用户是否存在。
		s.deps.Hasher.Compare(dummyBcryptHash, password)
		return nil, errInvalidCredentials
	}
	if !s.deps.Hasher.Compare(u.PasswordHash(), password) {
		return nil, errInvalidCredentials
	}
	if !u.CanLogin() {
		return nil, errInvalidCredentials
	}

	token, err := s.deps.Generator.SessionToken()
	if err != nil {
		return nil, err
	}
	sess, err := user.NewSession(user.SessionSpec{
		TokenHash: s.deps.Secrets.Hash(token),
		UserID:    u.ID(),
		Now:       now,
		TTL:       s.deps.SessionTTL,
	})
	if err != nil {
		return nil, err
	}
	if err := s.deps.Sessions.Save(ctx, sess); err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresAt: sess.ExpiresAt(), User: toUserView(u)}, nil
}

// Logout 删除当前会话（幂等：会话已不存在也视为成功）。
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return s.deps.Sessions.Delete(ctx, s.deps.Secrets.Hash(rawToken))
}

// AuthenticateSession 校验会话令牌并返回当前用户；过期会话顺手清理。
func (s *Service) AuthenticateSession(ctx context.Context, rawToken string) (*user.User, error) {
	if rawToken == "" {
		return nil, errInvalidCredentials
	}
	sess, err := s.deps.Sessions.FindByTokenHash(ctx, s.deps.Secrets.Hash(rawToken))
	if err != nil {
		return nil, errInvalidCredentials
	}
	now := s.deps.Clock.Now()
	if sess.IsExpired(now) {
		_ = s.deps.Sessions.Delete(ctx, sess.TokenHash())
		return nil, errInvalidCredentials
	}
	u, err := s.deps.Users.FindByID(ctx, sess.UserID())
	if err != nil {
		return nil, errInvalidCredentials
	}
	if !u.CanLogin() {
		return nil, errInvalidCredentials
	}
	return u, nil
}
