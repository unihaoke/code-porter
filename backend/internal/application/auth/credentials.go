package auth

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// CredentialService 对外秘钥鉴权（/v1 与 /agent 共用）。
//
// 失败语义：
//   * 秘钥不存在/已删除/已过期 → 401（unauthorized，不区分具体原因）
//   * 秘钥有效但缺少所要求 scope → 403（forbidden）
//   * 属主账号被停用 → 401

// AuthenticateAPISecret 校验明文秘钥并返回属主与秘钥聚合。
func (s *Service) AuthenticateAPISecret(ctx context.Context, secret string, scope apikey.Scope) (*user.User, *apikey.APIKey, error) {
	if secret == "" {
		return nil, nil, errInvalidCredentials
	}
	keyHash := s.deps.Secrets.Hash(secret)
	k, err := s.deps.Keys.FindByHash(ctx, keyHash)
	if err != nil {
		return nil, nil, errInvalidCredentials
	}
	now := s.deps.Clock.Now()
	if k.Expired(now) {
		return nil, nil, errInvalidCredentials
	}
	if !k.HasScope(scope) {
		return nil, nil, errScopeDenied
	}
	owner, err := s.deps.Users.FindByID(ctx, k.OwnerID())
	if err != nil || !owner.CanLogin() {
		return nil, nil, errInvalidCredentials
	}
	s.touchUsage(ctx, k, now)
	return owner, k, nil
}

// touchUsage 节流更新 last_used_at：同一秘钥在节流窗口内只落库一次。
func (s *Service) touchUsage(ctx context.Context, k *apikey.APIKey, now time.Time) {
	s.mu.Lock()
	last, ok := s.useTouch[k.KeyHash()]
	if ok && now.Sub(last) < s.deps.KeyUseThrottle {
		s.mu.Unlock()
		return
	}
	s.useTouch[k.KeyHash()] = now
	s.mu.Unlock()

	k.MarkUsed(now)
	_ = s.deps.Keys.Save(ctx, k) // 最佳努力：统计落库失败不影响鉴权
}

func (s *Service) clearTouch(keyHash string) {
	s.mu.Lock()
	delete(s.useTouch, keyHash)
	s.mu.Unlock()
}
