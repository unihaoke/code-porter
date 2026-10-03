// Package bot 实现 IM 机器人渠道的基础设施：回调解析、消息加解密与出站推送。
package bot

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrBadSignature 签名校验失败。
var ErrBadSignature = errors.New("bot: bad signature")

// ErrBadAESKey 加密密钥非法。
var ErrBadAESKey = errors.New("bot: invalid encoding aes key")

// decodeAESKey 把 43 位 Base64 密钥还原为 32 字节密钥。
//
// 飞书与企业微信的 EncodingAESKey 都是「32 字节 Base64 后去掉尾部 = 」，长度为 43。
func decodeAESKey(key string) ([]byte, error) {
	raw := strings.TrimSpace(key)
	if raw == "" {
		return nil, ErrBadAESKey
	}
	raw += "="
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadAESKey, err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes, want 32", ErrBadAESKey, len(b))
	}
	return b, nil
}

// pkcs7Pad 按 PKCS#7 填充到块大小整数倍。
func pkcs7Pad(data []byte, block int) []byte {
	pad := block - len(data)%block
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// pkcs7Unpad 去除 PKCS#7 填充。
func pkcs7Unpad(data []byte, block int) ([]byte, error) {
	if len(data) == 0 || len(data)%block != 0 {
		return nil, errors.New("bot: invalid padded data length")
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > block || pad > len(data) {
		return nil, errors.New("bot: invalid padding")
	}
	for i := len(data) - pad; i < len(data); i++ {
		if data[i] != byte(pad) {
			return nil, errors.New("bot: invalid padding bytes")
		}
	}
	return data[:len(data)-pad], nil
}

// aesCBCEncrypt AES-256-CBC 加密。
func aesCBCEncrypt(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cbcEncrypt(block, iv, pkcs7Pad(data, block.BlockSize()))
}

// aesCBCDecrypt AES-256-CBC 解密并去除填充。
func aesCBCDecrypt(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return nil, errors.New("bot: cipher text length not aligned")
	}
	out := make([]byte, len(data))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(out, data)
	return pkcs7Unpad(out, block.BlockSize())
}

func cbcEncrypt(block cipher.Block, iv, data []byte) ([]byte, error) {
	if len(iv) != block.BlockSize() {
		return nil, errors.New("bot: iv length mismatch")
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	return out, nil
}

// randomBytes 生成 n 字节随机串。
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// --- 飞书 ---

// feishuDecrypt 解密飞书加密事件。
//
// 密文结构：Base64( 16 字节随机 IV + AES-256-CBC(SHA256(EncodingAESKey), IV, 明文) )。
func feishuDecrypt(aesKey, cipherText string) ([]byte, error) {
	key, err := decodeAESKey(aesKey)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return nil, fmt.Errorf("bot: decode feishu cipher: %w", err)
	}
	if len(raw) <= aes.BlockSize {
		return nil, errors.New("bot: feishu cipher too short")
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(aesKey)))
	_ = key // 飞书使用 SHA256(EncodingAESKey 字符串) 作为密钥
	return aesCBCDecrypt(sum[:], raw[:aes.BlockSize], raw[aes.BlockSize:])
}

// feishuSign 计算自定义机器人 Webhook 签名：md5(timestamp + "\n" + secret)。
func feishuSign(secret, timestamp string) string {
	h := md5.Sum([]byte(timestamp + "\n" + secret))
	return fmt.Sprintf("%x", h)
}

// --- 企业微信 ---

// wecomKey 返回企微加解密密钥（即 EncodingAESKey 的 32 字节原文）。
func wecomKey(aesKey string) ([]byte, error) { return decodeAESKey(aesKey) }

// wecomDecrypt 解密企微回调密文，并校验末尾的 receiveid（CorpID）。
//
// 明文结构：16 字节随机串 + 4 字节网络序长度 + 消息体 + receiveid。
func wecomDecrypt(aesKey, cipherText, expectReceiveID string) ([]byte, error) {
	key, err := wecomKey(aesKey)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return nil, fmt.Errorf("bot: decode wecom cipher: %w", err)
	}
	plain, err := aesCBCDecrypt(key, key[:aes.BlockSize], raw)
	if err != nil {
		return nil, err
	}
	if len(plain) < 20 {
		return nil, errors.New("bot: wecom plain text too short")
	}
	msgLen := int(binary.BigEndian.Uint32(plain[16:20]))
	if msgLen < 0 || 20+msgLen > len(plain) {
		return nil, errors.New("bot: wecom message length out of range")
	}
	msg := plain[20 : 20+msgLen]
	receiveID := string(plain[20+msgLen:])
	if expectReceiveID != "" && receiveID != expectReceiveID {
		return nil, fmt.Errorf("bot: wecom receive id mismatch: got %q", receiveID)
	}
	return msg, nil
}

// wecomEncrypt 加密明文（用于 URL 验证回显 echostr）。
func wecomEncrypt(aesKey, receiveID, plain string) (string, error) {
	key, err := wecomKey(aesKey)
	if err != nil {
		return "", err
	}
	rnd, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	body := []byte(plain)
	buf := make([]byte, 0, 20+len(body)+len(receiveID))
	buf = append(buf, rnd...)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(body)))
	buf = append(buf, lenBuf...)
	buf = append(buf, body...)
	buf = append(buf, []byte(receiveID)...)
	cipherText, err := aesCBCEncrypt(key, key[:aes.BlockSize], buf)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

// wecomSign 计算企微回调签名：SHA1(字典序排序后的 token、timestamp、nonce、密文 拼接串)。
func wecomSign(token, timestamp, nonce, encrypted string) string {
	parts := []string{token, timestamp, nonce, encrypted}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", h)
}
