// Package crypto 提供敏感字段（数据库口令、SSH 私钥等）的对称加解密工具。
//
// 设计要点：
//   - 算法 AES-256-GCM（机密性 + 完整性，标准库支持）；
//   - 主密钥不直接使用，先经 SHA-256 派生固定 32 字节密钥；
//   - 每条密文使用 crypto/rand 生成独立 12 字节 nonce，禁止复用；
//   - 密文以 "v1:" 版本前缀 + base64(nonce|ciphertext|tag) 编码，便于未来算法轮换；
//   - 空明文视为未设置的字段，原样返回空串，避免 NOT NULL 语义歧义。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const versionPrefix = "v1:"

// Cipher 线程安全的 AES-GCM 加解密器（gcm 本身可并发使用）。
type Cipher struct {
	gcm cipher.AEAD
}

// NewCipher 以任意长度主密钥（建议 >= 32 字节）构造加解密器。
func NewCipher(masterSecret []byte) (*Cipher, error) {
	if len(masterSecret) == 0 {
		return nil, errors.New("加密主密钥不能为空")
	}
	key := sha256.Sum256(masterSecret) // SHA-256 输出恰好 32 字节，满足 AES-256
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("创建 AES 分组密码失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 模式失败: %w", err)
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt 加密明文，返回 v1 前缀的 base64 字符串。
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	sealed := c.gcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := append(nonce, sealed...)
	return versionPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// Decrypt 解密 Encrypt 产出的字符串；空串原样返回。
func (c *Cipher) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, versionPrefix) {
		return "", errors.New("密文版本或格式不受支持")
	}
	combined, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, versionPrefix))
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	ns := c.gcm.NonceSize()
	if len(combined) < ns+1 {
		return "", errors.New("密文长度非法")
	}
	nonce, ciphertext := combined[:ns], combined[ns:]
	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("GCM 解密失败（密钥不匹配或数据被篡改）: %w", err)
	}
	return string(plaintext), nil
}
