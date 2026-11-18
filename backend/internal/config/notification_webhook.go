package config

import (
	"errors"
	"net/url"
)

type NotificationWebhookConfig struct{ URL, Secret string }

func ValidateNotificationWebhook(c NotificationWebhookConfig, allowLocalHTTP bool) error {
	if c.URL == "" && c.Secret == "" {
		return nil
	}
	u, err := url.Parse(c.URL)
	if err != nil || len(c.URL) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" || len(c.Secret) < 32 || len(c.Secret) > 4096 {
		return errors.New("外部投递需要有效的固定地址及至少32字符的签名密钥")
	}
	if u.Scheme != "https" && !(allowLocalHTTP && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return errors.New("外部投递必须使用HTTPS；开发环境仅允许回环HTTP")
	}
	return nil
}
