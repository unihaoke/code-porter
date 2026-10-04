package http

import (
	"net/http"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// UsersHandlers 用户管理与个人密码接口。
type UsersHandlers struct {
	svc *authsvc.Service
	log port.Logger
}

// NewUsersHandlers 构造处理器。
func NewUsersHandlers(svc *authsvc.Service, log port.Logger) *UsersHandlers {
	return &UsersHandlers{svc: svc, log: log.With(port.F("h", "users"))}
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role,omitempty"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// List 列出全部用户（admin）。
func (h *UsersHandlers) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListUsers(r.Context(), userFromContext(r.Context()))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": list})
}

// Create 创建用户（admin）。
func (h *UsersHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.svc.CreateUser(r.Context(), userFromContext(r.Context()), req.Username, req.Password, req.Role)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": view})
}

// Delete 删除用户（admin）。
func (h *UsersHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := user.ID(r.PathValue("id"))
	if err := h.svc.DeleteUser(r.Context(), userFromContext(r.Context()), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ResetPassword 管理员重置他人密码（admin）。
func (h *UsersHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	id := user.ID(r.PathValue("id"))
	if err := h.svc.ResetPassword(r.Context(), userFromContext(r.Context()), id, req.Password); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ChangePassword 当前用户修改自己的密码。
func (h *UsersHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	currentHash := h.svc.HashSecret(sessionToken(r))
	if err := h.svc.ChangePassword(r.Context(), userFromContext(r.Context()),
		req.OldPassword, req.NewPassword, currentHash); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
