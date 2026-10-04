package auth

import (
	"context"
	"time"

	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// CreateKey 创建秘钥。明文秘钥只在本返回值中出现一次。
//
// permission 为通过该秘钥下发任务的文件操作权限上限（read/write/all），
// 空串按 all 处理；非法值返回领域校验错误。
func (s *Service) CreateKey(ctx context.Context, actor *user.User, name string, scopes []string,
	permission string, expiresAt *time.Time) (*CreatedKeyResult, error) {
	if actor == nil {
		return nil, errInvalidCredentials
	}
	secret, err := s.deps.Generator.APIKey()
	if err != nil {
		return nil, err
	}
	scopeList := parseScopes(scopes)
	perm := apikey.Permission(permission)
	if perm == "" {
		perm = apikey.PermissionAll
	}
	k, err := apikey.NewKey(apikey.Spec{
		UserID:     actor.ID(),
		Name:       name,
		Scopes:     scopeList,
		Permission: perm,
		KeyHash:    s.deps.Secrets.Hash(secret),
		Prefix:     prefixOf(secret),
		ExpiresAt:  expiresAt,
		Now:        s.deps.Clock.Now(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.deps.Keys.Save(ctx, k); err != nil {
		return nil, err
	}
	return &CreatedKeyResult{
		KeyView: toKeyView(k, s.deps.Clock.Now()),
		Secret:  secret,
	}, nil
}

// ListKeys 列出秘钥：targetID 为空查自己，非空需 admin。
func (s *Service) ListKeys(ctx context.Context, actor *user.User, targetID user.ID) ([]KeyView, error) {
	if actor == nil {
		return nil, errInvalidCredentials
	}
	if targetID != "" && targetID != actor.ID() {
		if err := requireAdmin(actor); err != nil {
			return nil, err
		}
	} else {
		targetID = actor.ID()
	}
	list, err := s.deps.Keys.ListByUser(ctx, targetID)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock.Now()
	out := make([]KeyView, 0, len(list))
	for _, k := range list {
		out = append(out, toKeyView(k, now))
	}
	return out, nil
}

// DeleteKey 删除秘钥：targetID 为空删自己的，非空需 admin；任何不匹配都返回 not found。
func (s *Service) DeleteKey(ctx context.Context, actor *user.User, targetID user.ID, keyID apikey.ID) error {
	if actor == nil {
		return errInvalidCredentials
	}
	if targetID != "" && targetID != actor.ID() {
		if err := requireAdmin(actor); err != nil {
			return err
		}
	} else {
		targetID = actor.ID()
	}
	k, err := s.deps.Keys.FindByID(ctx, keyID)
	if err != nil {
		return errKeyNotFound
	}
	if k.OwnerID() != targetID {
		return errKeyNotFound
	}
	if err := s.deps.Keys.Delete(ctx, keyID); err != nil {
		return err
	}
	// 删除后立即失效 last_used 节流记录，无副作用。
	s.clearTouch(k.KeyHash())
	return nil
}

// parseScopes 空集 → 默认双 scope；否则白名单转换，非法值交由领域校验拒绝。
func parseScopes(in []string) []apikey.Scope {
	if len(in) == 0 {
		return apikey.AllScopes
	}
	out := make([]apikey.Scope, 0, len(in))
	for _, raw := range in {
		out = append(out, apikey.Scope(raw))
	}
	return out
}

// prefixOf 取明文前缀用于列表辨识（长度由领域常量约束）。
func prefixOf(secret string) string {
	if len(secret) <= apikey.PrefixLen {
		return secret
	}
	return secret[:apikey.PrefixLen]
}
