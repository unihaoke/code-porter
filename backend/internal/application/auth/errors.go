package auth

import "github.com/codeporter/code-porter/pkg/apperr"

// 用例层统一错误。登录失败刻意使用同一文案，避免用户名枚举。
var (
	errInvalidCredentials = apperr.New(apperr.CodeUnauthorized, "用户名或密码错误")
	errAdminOnly          = apperr.New(apperr.CodeForbidden, "admin only")
	errCannotDeleteSelf   = apperr.New(apperr.CodeInvalidParam, "cannot delete yourself")
	errKeyNotFound        = apperr.New(apperr.CodeNotFound, "api key not found")
	errUserNotFound       = apperr.New(apperr.CodeNotFound, "user not found")
	// errScopeDenied 秘钥有效但缺少所要求 scope → 403。
	errScopeDenied = apperr.New(apperr.CodeForbidden, "api key scope not allowed")
)
