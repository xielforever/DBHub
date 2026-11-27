package auth

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestTOTPRFC6238VectorsAndReplayBoundaries(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, vector := range []struct {
		seconds int64
		code    string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}} {
		if got := totpCode(key, vector.seconds/30, 8); got != vector.code {
			t.Fatalf("RFC vector mismatch at %d", vector.seconds)
		}
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)
	now := time.Unix(2000000000, 0)
	code := totpCode(key, now.Unix()/30, 6)
	step, ok := verifyTOTP(secret, code, -1, now)
	if !ok || step != now.Unix()/30 {
		t.Fatal("current code rejected")
	}
	if _, ok = verifyTOTP(secret, code, step, now); ok {
		t.Fatal("consumed counter accepted again")
	}
	if _, ok = verifyTOTP(secret, code, -1, now.Add(61*time.Second)); ok {
		t.Fatal("code outside the resynchronization window accepted")
	}
	for _, bad := range []string{"", "1234567", "１２３４５６", "00000 ", "00000x"} {
		if _, ok = verifyTOTP(secret, bad, -1, now); ok {
			t.Fatal("malformed code accepted")
		}
	}
	generated, err := newTOTPSecret()
	if err != nil || len(generated) != 32 || generated == secret {
		t.Fatal("secret generation failed")
	}
}
