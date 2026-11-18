package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"time"
)

// TOTP follows RFC 6238: one unique 160-bit key, HMAC-SHA1, six digits and a
// 30-second step. The store atomically consumes the returned counter; validation
// alone must never be treated as consumption of a one-time code.
func newTOTPSecret() (string, error) {
	key := make([]byte, 20)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key), nil
}
func totpCode(key []byte, counter int64, digits int) string {
	var moving [8]byte
	binary.BigEndian.PutUint64(moving[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(moving[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	number := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	modulus := uint32(1000000)
	if digits == 8 {
		modulus = 100000000
	}
	return fmt.Sprintf("%0*d", digits, number%modulus)
}
func verifyTOTP(secret, code string, lastCounter int64, now time.Time) (int64, bool) {
	if len(secret) != 32 || len(code) != 6 || now.Unix() < 30 {
		return 0, false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) != 20 {
		return 0, false
	}
	step := now.Unix() / 30
	for _, counter := range []int64{step, step - 1, step + 1} {
		if counter > lastCounter && hmac.Equal([]byte(totpCode(key, counter, 6)), []byte(code)) {
			return counter, true
		}
	}
	return 0, false
}

// The caller must consume this counter in the same transaction as its proof.
func VerifyGovernanceTOTP(secret, code string, lastCounter int64, now time.Time) (int64, bool) {
	return verifyTOTP(secret, code, lastCounter, now)
}
