package task

import "strings"

// Permission 任务被授予的本地文件操作权限（随秘钥/请求下发，在本地 Agent 侧强制）。
//
// 三档语义：
//   - PermissionRead 只读：只能阅读、分析、搜索代码，禁止任何写入与副作用命令；
//   - PermissionWrite 可写：允许在工作目录内增改文件，禁止工作区外写入与系统命令；
//   - PermissionAll 全部：读写与命令执行均放开（无人值守全自动）。
type Permission string

const (
	// PermissionRead 只读。
	PermissionRead Permission = "read"
	// PermissionWrite 可写（工作目录内）。
	PermissionWrite Permission = "write"
	// PermissionAll 全部权限（默认，向后兼容）。
	PermissionAll Permission = "all"
)

// Valid 是否为合法权限值。
func (p Permission) Valid() bool {
	return p == PermissionRead || p == PermissionWrite || p == PermissionAll
}

// String 权限字符串。
func (p Permission) String() string { return string(p) }

// rank 权限强度：read < write < all，用于「秘钥上限 + 请求收紧」取交集。
func (p Permission) rank() int {
	switch p {
	case PermissionRead:
		return 0
	case PermissionWrite:
		return 1
	default:
		return 2
	}
}

// ParsePermission 解析权限；空串按 all 处理（网页控制台/机器人等无秘钥入口的默认值），
// 非法值返回 ok=false 由调用方拒绝。
func ParsePermission(s string) (Permission, bool) {
	p := Permission(strings.ToLower(strings.TrimSpace(s)))
	if p == "" {
		return PermissionAll, true
	}
	if !p.Valid() {
		return "", false
	}
	return p, true
}

// RestrictPermission 返回两个权限中更严格者。
// 秘钥给出权限上限，单次请求可通过 X-CodePorter-Permission 进一步收紧，
// 但永远不能突破秘钥上限（read 秘钥不可能靠请求头提权到 all）。
// 任一参数非法时按 all 参与比较。
func RestrictPermission(a, b Permission) Permission {
	if a.rank() <= b.rank() {
		return a
	}
	return b
}
