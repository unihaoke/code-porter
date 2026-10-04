// Package user 承载「控制台用户（租户）」聚合根：身份、角色、状态与密码哈希。
//
// 领域层只持有密码哈希（bcrypt 由基础设施层计算），不接触任何第三方库。
// 用户是多租户隔离的归属主体：秘钥、Agent 实例、任务、机器人均归属于某个用户。
package user

import (
	"regexp"
	"strings"
	"time"

	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// ID 用户唯一标识。
type ID string

// String 返回字符串形式。
func (i ID) String() string { return string(i) }

// NewID 生成用户 ID。
func NewID() ID { return ID(id.New("usr_")) }

// SeedAdminID 初始迁移种子 admin 的固定 ID（INSERT IGNORE 幂等所用）。
const SeedAdminID ID = "usr_admin_seed"

// Role 用户角色。
type Role string

const (
	// RoleAdmin 管理员：可管理用户并拥有全局视角。
	RoleAdmin Role = "admin"
	// RoleMember 普通用户：仅管理自己名下的资源。
	RoleMember Role = "member"
)

// String 返回字符串形式。
func (r Role) String() string { return string(r) }

// Valid 角色是否合法。
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember }

// Status 账号状态。
type Status string

const (
	// StatusActive 可正常登录。
	StatusActive Status = "active"
	// StatusDisabled 已停用，登录与凭据均被拒。
	StatusDisabled Status = "disabled"
)

// String 返回字符串形式。
func (s Status) String() string { return string(s) }

// 领域错误。
var (
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = apperr.New(apperr.CodeNotFound, "user not found")
	// ErrDuplicateUsername 用户名已存在（仓储层唯一约束冲突时使用）。
	ErrDuplicateUsername = apperr.New(apperr.CodeConflict, "username already exists")
	// ErrInvalidUsername 用户名不合法。
	ErrInvalidUsername = apperr.New(apperr.CodeInvalidParam, "invalid username")
	// ErrEmptyPassword 密码哈希为空。
	ErrEmptyPassword = apperr.New(apperr.CodeInvalidParam, "password hash is required")
	// ErrInvalidRole 角色不合法。
	ErrInvalidRole = apperr.New(apperr.CodeInvalidParam, "invalid role")
)

// usernamePattern 用户名规则：3–64 字符，小写字母/数字开头，允许下划线与中划线。
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,63}$`)

// NormalizeUsername 规整用户名：去空白并转小写。存储与查询前统一调用。
func NormalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateUsername 校验用户名是否符合规则（要求已是小写形态）。
func ValidateUsername(s string) error {
	if !usernamePattern.MatchString(s) {
		return ErrInvalidUsername
	}
	return nil
}

// Spec 创建用户的输入。
type Spec struct {
	// ID 为空时自动生成；种子 admin 使用固定 ID。
	ID ID
	// Username 调用方应先经 NormalizeUsername 规整。
	Username string
	// PasswordHash bcrypt 哈希，由应用层经 PasswordHasher 计算。
	PasswordHash string
	// Role 角色，缺省为 member。
	Role Role
	// Now 创建时间。
	Now time.Time
}

// User 用户聚合根。
type User struct {
	id           ID
	username     string
	passwordHash string
	role         Role
	status       Status
	createdAt    time.Time
	updatedAt    time.Time
}

// NewUser 创建处于 active 状态的用户。
func NewUser(spec Spec) (*User, error) {
	username := NormalizeUsername(spec.Username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.PasswordHash) == "" {
		return nil, ErrEmptyPassword
	}
	role := spec.Role
	if role == "" {
		role = RoleMember
	}
	if !role.Valid() {
		return nil, ErrInvalidRole
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	idValue := spec.ID
	if idValue == "" {
		idValue = NewID()
	}
	return &User{
		id:           idValue,
		username:     username,
		passwordHash: strings.TrimSpace(spec.PasswordHash),
		role:         role,
		status:       StatusActive,
		createdAt:    now,
		updatedAt:    now,
	}, nil
}

// Clone 返回副本（仓储以「取副本 → 修改 → 回写」方式避免数据竞争）。
func (u *User) Clone() *User {
	cp := *u
	return &cp
}

// --- 只读访问器 ---

// ID 标识。
func (u *User) ID() ID { return u.id }

// Username 用户名（已小写规整）。
func (u *User) Username() string { return u.username }

// PasswordHash 密码哈希（仅供应用层比对使用，严禁进入任何响应体）。
func (u *User) PasswordHash() string { return u.passwordHash }

// Role 角色。
func (u *User) Role() Role { return u.role }

// Status 状态。
func (u *User) Status() Status { return u.status }

// CreatedAt 创建时间。
func (u *User) CreatedAt() time.Time { return u.createdAt }

// UpdatedAt 最近更新时间。
func (u *User) UpdatedAt() time.Time { return u.updatedAt }

// IsAdmin 是否管理员。
func (u *User) IsAdmin() bool { return u.role == RoleAdmin }

// CanLogin 是否允许登录（账号须处于 active）。
func (u *User) CanLogin() bool { return u.status == StatusActive }

// --- 行为 ---

// ChangePassword 修改密码哈希。
func (u *User) ChangePassword(passwordHash string, now time.Time) error {
	if strings.TrimSpace(passwordHash) == "" {
		return ErrEmptyPassword
	}
	u.passwordHash = strings.TrimSpace(passwordHash)
	u.touch(now)
	return nil
}

// ChangeRole 修改角色。
func (u *User) ChangeRole(role Role, now time.Time) error {
	if !role.Valid() {
		return ErrInvalidRole
	}
	u.role = role
	u.touch(now)
	return nil
}

// Disable 停用账号。
func (u *User) Disable(now time.Time) {
	u.status = StatusDisabled
	u.touch(now)
}

// Enable 启用账号。
func (u *User) Enable(now time.Time) {
	u.status = StatusActive
	u.touch(now)
}

// touch 刷新更新时间。
func (u *User) touch(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	u.updatedAt = now
}

// Rewrite 回填仓储反序列化出来的字段。
//
// 仅供仓储使用：从持久化记录恢复聚合，绕过构造函数的默认值与生成逻辑。
func (u *User) Rewrite(id ID, username, passwordHash string, role Role, status Status, createdAt, updatedAt time.Time) {
	u.id = id
	u.username = username
	u.passwordHash = passwordHash
	u.role = role
	u.status = status
	u.createdAt = createdAt
	u.updatedAt = updatedAt
}
