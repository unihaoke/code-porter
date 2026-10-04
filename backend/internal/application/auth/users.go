package auth

import (
	"context"
	"strings"

	"github.com/codeporter/code-porter/internal/domain/user"
)

const minPasswordLen = 6

// ListUsers 列出全部用户（仅 admin）。
func (s *Service) ListUsers(ctx context.Context, actor *user.User) ([]UserView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	list, err := s.deps.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(list))
	for _, u := range list {
		out = append(out, toUserView(u))
	}
	return out, nil
}

// CreateUser 创建用户（仅 admin）。
func (s *Service) CreateUser(ctx context.Context, actor *user.User, username, password, role string) (*UserView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(password) == "" || len([]rune(password)) < minPasswordLen {
		return nil, user.ErrEmptyPassword
	}
	r := user.Role(strings.TrimSpace(role))
	if r == "" {
		r = user.RoleMember
	}
	if !r.Valid() {
		return nil, user.ErrInvalidRole
	}
	hash, err := s.deps.Hasher.Hash(password)
	if err != nil {
		return nil, err
	}
	u, err := user.NewUser(user.Spec{
		Username:     username,
		PasswordHash: hash,
		Role:         r,
		Now:          s.deps.Clock.Now(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.deps.Users.Save(ctx, u); err != nil {
		return nil, err
	}
	v := toUserView(u)
	return &v, nil
}

// DeleteUser 删除用户（仅 admin，不能删自己）；关联行由存储层外键级联。
func (s *Service) DeleteUser(ctx context.Context, actor *user.User, targetID user.ID) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if targetID == actor.ID() {
		return errCannotDeleteSelf
	}
	if _, err := s.deps.Users.FindByID(ctx, targetID); err != nil {
		return errUserNotFound
	}
	if err := s.deps.Users.Delete(ctx, targetID); err != nil {
		return err
	}
	// 外键级联清理秘钥/会话/实例身份后，再回收在线连接等运行时资源。
	if s.deps.AfterUserDelete != nil {
		s.deps.AfterUserDelete(ctx, targetID)
	}
	return nil
}

// ResetPassword 管理员重置他人密码，并清除该用户全部会话。
func (s *Service) ResetPassword(ctx context.Context, actor *user.User, targetID user.ID, newPassword string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if strings.TrimSpace(newPassword) == "" || len([]rune(newPassword)) < minPasswordLen {
		return user.ErrEmptyPassword
	}
	return s.applyPassword(ctx, targetID, newPassword, "")
}

// ChangePassword 用户修改自己的密码（需校验旧密码）；保留当前会话，其余会话失效。
func (s *Service) ChangePassword(ctx context.Context, actor *user.User, oldPassword, newPassword, currentSessionHash string) error {
	if !s.deps.Hasher.Compare(actor.PasswordHash(), oldPassword) {
		return errInvalidCredentials
	}
	if strings.TrimSpace(newPassword) == "" || len([]rune(newPassword)) < minPasswordLen {
		return user.ErrEmptyPassword
	}
	return s.applyPassword(ctx, actor.ID(), newPassword, currentSessionHash)
}

// applyPassword 落库新密码并清理会话（exceptHash 非空时保留该会话）。
func (s *Service) applyPassword(ctx context.Context, targetID user.ID, newPassword, exceptHash string) error {
	u, err := s.deps.Users.FindByID(ctx, targetID)
	if err != nil {
		return errUserNotFound
	}
	hash, err := s.deps.Hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	if err := u.ChangePassword(hash, s.deps.Clock.Now()); err != nil {
		return err
	}
	if err := s.deps.Users.Save(ctx, u); err != nil {
		return err
	}
	return s.deps.Sessions.DeleteByUserExcept(ctx, targetID, exceptHash)
}

// IsUsingDefaultPassword 判断种子 admin 是否仍在使用默认密码 admin123（启动告警用）。
func (s *Service) IsUsingDefaultPassword(ctx context.Context) bool {
	u, err := s.deps.Users.FindByID(ctx, user.SeedAdminID)
	if err != nil {
		return false
	}
	return s.deps.Hasher.Compare(u.PasswordHash(), defaultAdminPassword)
}

// defaultAdminPassword 迁移种子的默认密码。
const defaultAdminPassword = "admin123"
