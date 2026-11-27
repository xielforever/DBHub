package auth

import "testing"

func TestSSOReturnPath(t *testing.T) {
	const destination = "/profile?workspace_id=1#security"
	if got := ssoReturnPath(destination); got != destination {
		t.Fatalf("got %q, expected %q", got, destination)
	}
	for _, value := range []string{"", "profile", "https://external.invalid", "//external.invalid", "/a%00b", "/a%5Cb"} {
		if got := ssoReturnPath(value); got != "" {
			t.Errorf("unexpected accepted return path %q", got)
		}
	}
}
