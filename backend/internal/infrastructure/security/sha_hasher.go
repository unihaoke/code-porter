package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"

	"github.com/codeporter/code-porter/internal/application/port"
)

// SHA256Hasher 凭据摘要器：秘钥与会话令牌仅以 SHA-256 hex 形式入库。
type SHA256Hasher struct{}

var _ port.SecretHasher = (*SHA256Hasher)(nil)

// NewSHA256Hasher 构造摘要器。
func NewSHA256Hasher() *SHA256Hasher { return &SHA256Hasher{} }

// Hash 返回明文的 SHA-256 十六进制摘要（64 字符）。
func (h *SHA256Hasher) Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// EqualHash 常量时间比较「存储的摘要」与「明文实时摘要」。
func (h *SHA256Hasher) EqualHash(hash, secret string) bool {
	if hash == "" || secret == "" {
		return false
	}
	got := h.Hash(secret)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1
}
