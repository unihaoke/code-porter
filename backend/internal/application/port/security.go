// 安全相关出站端口：密码哈希、随机凭据生成、秘钥摘要。
//
// 应用层只依赖这些接口，具体算法（bcrypt / crypto/rand / SHA-256）
// 由 infrastructure/security 提供，领域层与应用层都不直接 import 加密三方库。
package port

// PasswordHasher 密码哈希与校验。
type PasswordHasher interface {
	// Hash 生成密码哈希（bcrypt）。
	Hash(password string) (string, error)
	// Compare 校验明文密码与哈希是否匹配；任何异常均返回 false。
	Compare(hash, password string) bool
}

// SecretHasher 凭据（秘钥/会话令牌）摘要与常量时间比较。
type SecretHasher interface {
	// Hash 返回明文的 SHA-256 hex 摘要。
	Hash(secret string) string
	// Equal 常量时间比较「摘要」与「明文摘要」是否相等。
	EqualHash(hash, secret string) bool
}

// CredentialGenerator 随机凭据生成。
type CredentialGenerator interface {
	// APIKey 生成对外秘钥明文：cp_ + 32 字节 URL-safe 随机串（RawURLEncoding，无 padding）。
	APIKey() (string, error)
	// SessionToken 生成会话令牌明文：32 字节随机量的十六进制（64 字符）。
	SessionToken() (string, error)
}
