package crypto

import "testing"

func TestCredentialCommandFingerprint(t *testing.T) {
	first, _ := NewCipher([]byte("first application secret"))
	same, _ := NewCipher([]byte("first application secret"))
	other, _ := NewCipher([]byte("another application secret"))
	payload := []byte(`{"password":"example"}`)
	if first.Fingerprint(payload) != same.Fingerprint(payload) {
		t.Fatal("retry fingerprint changed across restart")
	}
	if first.Fingerprint(payload) == other.Fingerprint(payload) || first.Fingerprint(payload) == first.Fingerprint([]byte(`{"password":"changed"}`)) {
		t.Fatal("fingerprint did not bind the secret and command")
	}
	encrypted, e := first.Encrypt("db-credential")
	if e != nil {
		t.Fatal(e)
	}
	plain, e := same.Decrypt(encrypted)
	if e != nil || plain != "db-credential" {
		t.Fatal("credential encryption compatibility changed")
	}
}
