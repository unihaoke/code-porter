// Package security 提供认证体系的加密基础设施：bcrypt 密码哈希、
// crypto/rand 随机凭据生成、SHA-256 凭据摘要。
//
// 这些类型实现 application/port 中的端口，供应用层装配，业务代码不直接引用本包。
package security

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"

	"github.com/codeporter/code-porter/internal/application/port"
	"golang.org/x/crypto/bcrypt"
)

// BcryptCost 与验收口径一致（≥10）。
const BcryptCost = 10

// BcryptHasher 基于 bcrypt 的密码哈希器。
type BcryptHasher struct {
	cost int
}

// 编译期断言：实现端口接口。
var _ port.PasswordHasher = (*BcryptHasher)(nil)

// NewBcryptHasher 构造哈希器。
func NewBcryptHasher() *BcryptHasher {
	return &BcryptHasher{cost: BcryptCost}
}

// Hash 生成 bcrypt 哈希。
func (h *BcryptHasher) Hash(password string) (string, error) {
	raw, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Compare 校验密码；哈希或密码非法时统一返回 false，不向调用方泄露差异。
// bcrypt.CompareHashAndPassword 内部为常量时间比较。
func (h *BcryptHasher) Compare(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// RandomGenerator 基于 crypto/rand 的凭据生成器。
type RandomGenerator struct{}

var _ port.CredentialGenerator = (*RandomGenerator)(nil)

// NewRandomGenerator 构造生成器。
func NewRandomGenerator() *RandomGenerator { return &RandomGenerator{} }

const (
	apiKeyBytes    = 32
	sessionBytes   = 32
	apiKeyPrefixV1 = "cp_"
)

// APIKey 生成 cp_ 前缀的秘钥明文（32 字节 RawURLEncoding，43 字符）。
func (g *RandomGenerator) APIKey() (string, error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return apiKeyPrefixV1 + base64.RawURLEncoding.EncodeToString(buf), nil
}

// SessionToken 生成十六进制会话令牌（32 字节，64 字符）。
func (g *RandomGenerator) SessionToken() (string, error) {
	buf := make([]byte, sessionBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
