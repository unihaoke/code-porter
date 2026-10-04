package http

import (
	"net/http"
	"time"

	authsvc "github.com/codeporter/code-porter/internal/application/auth"
	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/apikey"
	"github.com/codeporter/code-porter/internal/domain/user"
	"github.com/codeporter/code-porter/pkg/apperr"
)

// KeysHandlers 秘钥管理接口。
type KeysHandlers struct {
	svc *authsvc.Service
	log port.Logger
}

// NewKeysHandlers 构造处理器。
func NewKeysHandlers(svc *authsvc.Service, log port.Logger) *KeysHandlers {
	return &KeysHandlers{svc: svc, log: log.With(port.F("h", "keys"))}
}

type createKeyRequest struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes,omitempty"`
	Permission string   `json:"permission,omitempty"` // read | write | all；空=all
	ExpiresAt  string   `json:"expires_at,omitempty"` // RFC3339；空串/缺省=永久
}

// Create 创建秘钥（明文仅此一次返回）。
func (h *KeysHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	expires, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.svc.CreateKey(r.Context(), userFromContext(r.Context()),
		req.Name, req.Scopes, req.Permission, expires)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": res})
}

// ListMine 列出当前用户秘钥。
func (h *KeysHandlers) ListMine(w http.ResponseWriter, r *http.Request) {
	h.listFor(w, r, "")
}

// DeleteMine 删除当前用户的秘钥。
func (h *KeysHandlers) DeleteMine(w http.ResponseWriter, r *http.Request) {
	h.deleteFor(w, r, "", apikey.ID(r.PathValue("id")))
}

// ListUser 管理员查看指定用户秘钥。
func (h *KeysHandlers) ListUser(w http.ResponseWriter, r *http.Request) {
	h.listFor(w, r, user.ID(r.PathValue("id")))
}

// DeleteUser 管理员删除指定用户秘钥。
func (h *KeysHandlers) DeleteUser(w http.ResponseWriter, r *http.Request) {
	h.deleteFor(w, r, user.ID(r.PathValue("id")), apikey.ID(r.PathValue("keyId")))
}

func (h *KeysHandlers) listFor(w http.ResponseWriter, r *http.Request, target user.ID) {
	list, err := h.svc.ListKeys(r.Context(), userFromContext(r.Context()), target)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": list})
}

func (h *KeysHandlers) deleteFor(w http.ResponseWriter, r *http.Request, target user.ID, id apikey.ID) {
	if err := h.svc.DeleteKey(r.Context(), userFromContext(r.Context()), target, id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// parseExpiresAt 解析 RFC3339 过期时间；空串表示永不过期。
func parseExpiresAt(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, apperr.New(apperr.CodeInvalidParam, "expires_at must be RFC3339, e.g. 2026-12-31T23:59:59Z")
	}
	return &t, nil
}
