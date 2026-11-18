package auth

import (
	"encoding/base64"
	"net/url"

	qrcode "github.com/skip2/go-qrcode"
)

// The enrollment key is provisioned as an image, never printed in a page or log.
// Callers must authorize the current user and send no-store on this response.
func totpEnrollmentPNG(secret, username string) (string, error) {
	query := url.Values{"secret": {secret}, "issuer": {"DBHub"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	uri := "otpauth://totp/" + url.PathEscape("DBHub:"+username) + "?" + query.Encode()
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
