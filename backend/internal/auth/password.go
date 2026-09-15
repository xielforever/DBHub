// Package auth 提供密码哈希、JWT 令牌签发解析与认证处理器。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	pbkdf2Algo       = "pbkdf2_sha256"
	pbkdf2Iterations = 210_000
	pbkdf2SaltBytes  = 16
	pbkdf2KeyBytes   = 32
)

var b64 = base64.RawStdEncoding

// HashPassword 使用 PBKDF2-HMAC-SHA256 派生密码哈希。
// 编码格式：pbkdf2_sha256$<迭代次数>$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐值失败: %w", err)
	}
	dk, err := pbkdf2Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyBytes)
	if err != nil {
		return "", fmt.Errorf("密码派生失败: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		pbkdf2Algo, pbkdf2Iterations,
		b64.EncodeToString(salt), b64.EncodeToString(dk)), nil
}

// VerifyPassword 以恒定时间比较方式校验密码。
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != pbkdf2Algo {
		return false, errors.New("密码哈希格式不受支持")
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil {
		return false, errors.New("密码哈希迭代次数非法")
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false, errors.New("密码哈希盐值非法")
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil {
		return false, errors.New("密码哈希值非法")
	}
	got, err := pbkdf2Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, fmt.Errorf("密码派生失败: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
