package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 令牌类型。
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
	issuerName       = "dbhub"
)

var tokenEncoding = base64.RawURLEncoding

// Claims JWT 自定义声明。
type Claims struct {
	Issuer   string `json:"iss"`
	Subject  int64  `json:"sub"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Type     string `json:"typ"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	JWTID    string `json:"jti"`
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// TokenPair 一次登录签发的令牌对。
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// IssueToken 签发一枚 HS256 JWT。
func IssueToken(secret []byte, user *User, typ string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(ttl)
	jti := make([]byte, 8)
	if _, err := rand.Read(jti); err != nil {
		return "", time.Time{}, fmt.Errorf("生成 jti 失败: %w", err)
	}

	header := jwtHeader{Algorithm: "HS256", Type: "JWT"}
	claims := Claims{
		Issuer:   issuerName,
		Subject:  user.ID,
		Username: user.Username,
		Role:     user.Role,
		Type:     typ,
		IssuedAt: now.Unix(),
		Expires:  expires.Unix(),
		JWTID:    base64.RawURLEncoding.EncodeToString(jti),
	}

	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", time.Time{}, err
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}

	signingInput := tokenEncoding.EncodeToString(headerBytes) + "." +
		tokenEncoding.EncodeToString(claimsBytes)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	signature := tokenEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, expires, nil
}

// IssuePair 签发 access + refresh 令牌对。
func IssuePair(secret []byte, user *User, accessTTL, refreshTTL time.Duration) (*TokenPair, error) {
	access, exp, err := IssueToken(secret, user, TokenTypeAccess, accessTTL)
	if err != nil {
		return nil, err
	}
	refresh, _, err := IssueToken(secret, user, TokenTypeRefresh, refreshTTL)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresAt: exp}, nil
}

// ParseToken 校验签名、有效期与令牌类型并返回 Claims。
func ParseToken(secret []byte, tokenStr, expectedType string) (*Claims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("令牌格式非法")
	}

	headerBytes, err := tokenEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("令牌头编码非法")
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return nil, errors.New("令牌头不受信任")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)
	gotSig, err := tokenEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("令牌签名编码非法")
	}
	if !hmac.Equal(expectedSig, gotSig) {
		return nil, errors.New("令牌签名校验失败")
	}

	claimsBytes, err := tokenEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("令牌声明编码非法")
	}
	var claims Claims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, errors.New("令牌声明解析失败")
	}
	if claims.Issuer != issuerName {
		return nil, errors.New("令牌签发者不匹配")
	}
	if claims.Type != expectedType {
		return nil, fmt.Errorf("令牌类型错误，期望 %s", expectedType)
	}
	if time.Now().Unix() >= claims.Expires {
		return nil, errors.New("令牌已过期")
	}
	return &claims, nil
}
