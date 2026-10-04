// Package apikey 承载「租户秘钥」聚合根。
//
// 秘钥是用户签发的可吊销凭据，明文仅在创建时返回一次，仓储只存 SHA-256 哈希与展示前缀。
// 同一把秘钥可勾选 agent（本地客户端接入）与 api（OpenAI 兼容调用）两个 scope；
// 秘钥证明「归属」，具体哪台机器由 Agent 实例 ID 区分。
package apikey

import (
	"strings"
	"time"

	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
	"github.com/codeporter/code-porter/pkg/id"
)

// ID 秘钥唯一标识。
type ID string

// String 返回字符串形式。
func (i ID) String() string { return string(i) }

// NewID 生成秘钥 ID。
func NewID() ID { return ID(id.New("key_")) }

// Scope 秘钥权限范围。
type Scope string

const (
	// ScopeAgent 允许本地客户端接入 /agent/*。
	ScopeAgent Scope = "agent"
	// ScopeAPI 允许调用 OpenAI 兼容接口 /v1/*。
	ScopeAPI Scope = "api"
)

// String 返回字符串形式。
func (s Scope) String() string { return string(s) }

// Valid 范围是否合法。
func (s Scope) Valid() bool { return s == ScopeAgent || s == ScopeAPI }

// AllScopes 全部合法 scope（创建秘钥缺省时使用）。
var AllScopes = []Scope{ScopeAgent, ScopeAPI}

// Permission 秘钥授予的本地文件操作权限（作用于通过该秘钥下发的任务）。
//
// 与 scope 是两个正交维度：scope 决定「能调哪类接口」（agent 接入 / api 调用），
// permission 决定「任务在本地机器上能动什么」：
//   - PermissionRead 只读：禁止改文件与副作用命令；
//   - PermissionWrite 可写：仅允许工作目录内写入；
//   - PermissionAll 全部：读写与命令执行放开（默认）。
//
// 字符串值与 domain/task.Permission 保持一致，应用层可直接互转。
type Permission string

const (
	// PermissionRead 只读权限。
	PermissionRead Permission = "read"
	// PermissionWrite 工作目录可写权限。
	PermissionWrite Permission = "write"
	// PermissionAll 全部权限（缺省）。
	PermissionAll Permission = "all"
)

// Valid 权限是否合法。
func (p Permission) Valid() bool {
	return p == PermissionRead || p == PermissionWrite || p == PermissionAll
}

// PrefixLen 列表展示用前缀长度（明文前 10 位，含 cp_ 前缀）。
const PrefixLen = 10

const (
	maxNameLen = 128
	hashLen    = 64 // SHA-256 hex
)

// 领域错误。
var (
	// ErrAPIKeyNotFound 秘钥不存在（删除/从未创建）。
	ErrAPIKeyNotFound = apperr.New(apperr.CodeNotFound, "api key not found")
	// ErrEmptyOwner 秘钥必须归属某个用户。
	ErrEmptyOwner = apperr.New(apperr.CodeInvalidParam, "api key owner is required")
	// ErrEmptyName 名称为空。
	ErrEmptyName = apperr.New(apperr.CodeInvalidParam, "api key name is required")
	// ErrNameTooLong 名称超长。
	ErrNameTooLong = apperr.New(apperr.CodeInvalidParam, "api key name is too long")
	// ErrEmptyHash 秘钥哈希为空。
	ErrEmptyHash = apperr.New(apperr.CodeInvalidParam, "api key hash is required")
	// ErrEmptyPrefix 展示前缀为空。
	ErrEmptyPrefix = apperr.New(apperr.CodeInvalidParam, "api key prefix is required")
	// ErrInvalidScope scope 非法或为空。
	ErrInvalidScope = apperr.New(apperr.CodeInvalidParam, "invalid api key scope")
	// ErrInvalidPermission 文件操作权限非法。
	ErrInvalidPermission = apperr.New(apperr.CodeInvalidParam, "invalid api key permission")
	// ErrExpiresInPast 有效期不能早于当前时间。
	ErrExpiresInPast = apperr.New(apperr.CodeInvalidParam, "api key expires_at must be in the future")
)

// Spec 创建秘钥的输入。
type Spec struct {
	// ID 为空时自动生成。
	ID ID
	// UserID 归属用户。
	UserID user.ID
	// Name 用户可读名称。
	Name string
	// Scopes 权限范围；非空且必须是合法 scope 的去重子集。
	Scopes []Scope
	// Permission 通过该秘钥下发任务时的本地文件操作权限上限；缺省 all。
	Permission Permission
	// KeyHash 明文秘钥的 SHA-256 hex（由应用层计算）。
	KeyHash string
	// Prefix 明文前 PrefixLen 位，用于列表辨识。
	Prefix string
	// ExpiresAt 过期时间；nil 表示永不过期。
	ExpiresAt *time.Time
	// Now 创建时间。
	Now time.Time
}

// APIKey 秘钥聚合根。删除采用硬删除：仓储中查不到即等价于吊销。
type APIKey struct {
	id         ID
	userID     user.ID
	name       string
	scopes     []Scope
	permission Permission
	keyHash    string
	prefix     string
	expiresAt  *time.Time
	lastUsedAt time.Time
	createdAt  time.Time
	updatedAt  time.Time
}

// NewKey 创建秘钥聚合。
func NewKey(spec Spec) (*APIKey, error) {
	if spec.UserID == "" {
		return nil, ErrEmptyOwner
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, ErrEmptyName
	}
	if len([]rune(name)) > maxNameLen {
		return nil, ErrNameTooLong
	}
	scopes, err := normalizeScopes(spec.Scopes)
	if err != nil {
		return nil, err
	}
	permission := spec.Permission
	if permission == "" {
		permission = PermissionAll
	}
	if !permission.Valid() {
		return nil, ErrInvalidPermission
	}
	keyHash := strings.TrimSpace(spec.KeyHash)
	if len(keyHash) != hashLen {
		return nil, ErrEmptyHash
	}
	prefix := strings.TrimSpace(spec.Prefix)
	if prefix == "" {
		return nil, ErrEmptyPrefix
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	if spec.ExpiresAt != nil && !spec.ExpiresAt.After(now) {
		return nil, ErrExpiresInPast
	}
	idValue := spec.ID
	if idValue == "" {
		idValue = NewID()
	}
	return &APIKey{
		id:         idValue,
		userID:     spec.UserID,
		name:       name,
		scopes:     scopes,
		permission: permission,
		keyHash:    keyHash,
		prefix:     prefix,
		expiresAt:  cloneTime(spec.ExpiresAt),
		createdAt:  now,
		updatedAt:  now,
	}, nil
}

// Clone 返回副本（含 scopes 切片与过期时间指针的深拷贝）。
func (k *APIKey) Clone() *APIKey {
	cp := *k
	if len(k.scopes) > 0 {
		cp.scopes = append([]Scope(nil), k.scopes...)
	}
	cp.expiresAt = cloneTime(k.expiresAt)
	return &cp
}

// --- 只读访问器 ---

// ID 标识。
func (k *APIKey) ID() ID { return k.id }

// OwnerID 归属用户 ID。
func (k *APIKey) OwnerID() user.ID { return k.userID }

// Name 名称。
func (k *APIKey) Name() string { return k.name }

// Scopes 权限范围副本。
func (k *APIKey) Scopes() []Scope { return append([]Scope(nil), k.scopes...) }

// Permission 通过该秘钥下发任务的文件操作权限上限。
func (k *APIKey) Permission() Permission {
	if k.permission == "" {
		return PermissionAll
	}
	return k.permission
}

// KeyHash 秘钥哈希（仅仓储鉴权路径使用，严禁进入响应体）。
func (k *APIKey) KeyHash() string { return k.keyHash }

// Prefix 展示前缀。
func (k *APIKey) Prefix() string { return k.prefix }

// ExpiresAt 过期时间值；零值（time.Time{}）表示永不过期，请配合 HasExpiry 判断。
func (k *APIKey) ExpiresAt() time.Time {
	if k.expiresAt == nil {
		return time.Time{}
	}
	return *k.expiresAt
}

// HasExpiry 是否设置了过期时间。
func (k *APIKey) HasExpiry() bool { return k.expiresAt != nil }

// LastUsedAt 最近一次鉴权使用时间。
func (k *APIKey) LastUsedAt() time.Time { return k.lastUsedAt }

// CreatedAt 创建时间。
func (k *APIKey) CreatedAt() time.Time { return k.createdAt }

// UpdatedAt 更新时间。
func (k *APIKey) UpdatedAt() time.Time { return k.updatedAt }

// --- 行为 ---

// HasScope 是否包含指定 scope。
func (k *APIKey) HasScope(scope Scope) bool {
	for _, s := range k.scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Expired 在给定时刻是否已过期（未设置过期时间永不过期）。
func (k *APIKey) Expired(now time.Time) bool {
	return k.expiresAt != nil && !now.Before(*k.expiresAt)
}

// Usable 在给定时刻是否可用于指定 scope：未过期且包含该 scope。
// 「已删除」无法通过此方法判断——硬删除后仓储查不到记录，鉴权在查询阶段即失败。
func (k *APIKey) Usable(now time.Time, scope Scope) bool {
	return !k.Expired(now) && k.HasScope(scope)
}

// MarkUsed 记录最近使用时间（允许应用层节流后调用）。
func (k *APIKey) MarkUsed(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	k.lastUsedAt = now
}

// Rewrite 回填仓储反序列化字段，仅供仓储使用。
func (k *APIKey) Rewrite(id ID, userID user.ID, name string, scopes []Scope, permission Permission,
	keyHash, prefix string, expiresAt *time.Time, lastUsedAt, createdAt, updatedAt time.Time) {
	k.id = id
	k.userID = userID
	k.name = name
	k.scopes = append([]Scope(nil), scopes...)
	if permission == "" {
		permission = PermissionAll
	}
	k.permission = permission
	k.keyHash = keyHash
	k.prefix = prefix
	k.expiresAt = cloneTime(expiresAt)
	k.lastUsedAt = lastUsedAt
	k.createdAt = createdAt
	k.updatedAt = updatedAt
}

// normalizeScopes 去重并校验 scope 集合：必须非空且全部合法。
func normalizeScopes(in []Scope) ([]Scope, error) {
	if len(in) == 0 {
		return nil, ErrInvalidScope
	}
	seen := make(map[Scope]struct{}, len(in))
	out := make([]Scope, 0, len(in))
	for _, s := range in {
		s = Scope(strings.TrimSpace(string(s)))
		if !s.Valid() {
			return nil, ErrInvalidScope
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, ErrInvalidScope
	}
	return out, nil
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
