package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// NacosOptions 描述 Nacos 配置源。启用后 Nacos 中的白名单键优先于环境变量。
type NacosOptions struct {
	Enabled      bool
	ServerAddr   string
	Namespace    string
	Group        string
	DataID       string
	Username     string
	Password     string
	Timeout      time.Duration
	PollInterval time.Duration
	Required     bool
	Format       string
}

// nacosWhitelist 限定可以从 Nacos 覆盖的键，避免远端配置意外改写其他运行参数。
var nacosWhitelist = map[string]bool{
	"DATABASE_URL": true,
	"REDIS_URL":    true,
}

const (
	nacosDefaultTimeout      = 3 * time.Second
	nacosDefaultPollInterval = 15 * time.Second
)

// loadNacosOptions 读取并校验 Nacos 相关环境变量。
func loadNacosOptions() (NacosOptions, error) {
	opts := NacosOptions{
		Enabled:      getenvBool("DBHUB_NACOS_ENABLED", false),
		ServerAddr:   strings.TrimSpace(os.Getenv("DBHUB_NACOS_SERVER_ADDR")),
		Namespace:    strings.TrimSpace(os.Getenv("DBHUB_NACOS_NAMESPACE")),
		Group:        strings.TrimSpace(getenv("DBHUB_NACOS_GROUP", "DEFAULT_GROUP")),
		DataID:       strings.TrimSpace(os.Getenv("DBHUB_NACOS_DATA_ID")),
		Username:     os.Getenv("DBHUB_NACOS_USERNAME"),
		Password:     os.Getenv("DBHUB_NACOS_PASSWORD"),
		Timeout:      nacosDefaultTimeout,
		PollInterval: nacosDefaultPollInterval,
		Required:     getenvBool("DBHUB_NACOS_REQUIRED", true),
		Format:       strings.ToLower(strings.TrimSpace(getenv("DBHUB_NACOS_FORMAT", "properties"))),
	}
	if !opts.Enabled {
		return opts, nil
	}
	if opts.ServerAddr == "" {
		return opts, errors.New("启用 Nacos 时必须配置 DBHUB_NACOS_SERVER_ADDR")
	}
	if opts.DataID == "" {
		return opts, errors.New("启用 Nacos 时必须配置 DBHUB_NACOS_DATA_ID")
	}
	if opts.Format != "properties" {
		return opts, errors.New("DBHUB_NACOS_FORMAT 目前只支持 properties")
	}
	if raw := os.Getenv("DBHUB_NACOS_TIMEOUT_MS"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 200 || ms > 60000 {
			return opts, errors.New("DBHUB_NACOS_TIMEOUT_MS 必须为 200–60000 的整数")
		}
		opts.Timeout = time.Duration(ms) * time.Millisecond
	}
	if raw := os.Getenv("DBHUB_NACOS_POLL_SECONDS"); raw != "" {
		secs, err := strconv.Atoi(raw)
		if err != nil || secs < 5 || secs > 3600 {
			return opts, errors.New("DBHUB_NACOS_POLL_SECONDS 必须为 5–3600 的整数")
		}
		opts.PollInterval = time.Duration(secs) * time.Second
	}
	return opts, nil
}

// FetchNacosValues 读取一次 Nacos 配置，返回白名单键值与原始内容哈希。
func FetchNacosValues(ctx context.Context, opts NacosOptions) (map[string]string, string, error) {
	content, err := newNacosClient(opts).getConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	return parseNacosProperties(content), hashContent(content), nil
}

// WatchNacos 轮询 Nacos，内容哈希变化时调用 onChange。onChange 返回 true 表示
// 变更已被接受（调用方通常会触发受控重启）；接受后 watcher 停止上报同一版本。
// 监听错误交给 onError，不中断监听。
func WatchNacos(
	ctx context.Context,
	opts NacosOptions,
	initialHash string,
	onChange func(values map[string]string, hash string) bool,
	onError func(error),
) {
	client := newNacosClient(opts)
	last := initialHash
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		content, err := client.getConfig(ctx)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			continue
		}
		hash := hashContent(content)
		if hash == last {
			continue
		}
		values := parseNacosProperties(content)
		if onChange != nil && onChange(values, hash) {
			last = hash
		}
	}
}

type nacosClient struct {
	opts  NacosOptions
	http  *http.Client
	token string
}

func newNacosClient(opts NacosOptions) *nacosClient {
	return &nacosClient{opts: opts, http: &http.Client{Timeout: opts.Timeout}}
}

func (c *nacosClient) baseURL() string {
	addr := strings.TrimSpace(c.opts.ServerAddr)
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return strings.TrimSuffix(addr, "/")
}

func (c *nacosClient) getConfig(ctx context.Context) (string, error) {
	if c.opts.Username != "" && c.token == "" {
		if err := c.login(ctx); err != nil {
			return "", err
		}
	}
	body, status, err := c.request(ctx, http.MethodGet, "/v3/console/cs/config", nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if c.opts.Username == "" {
			return "", fmt.Errorf("Nacos 配置读取被拒绝（HTTP %d），请检查账号与命名空间权限", status)
		}
		if err := c.login(ctx); err != nil {
			return "", err
		}
		body, status, err = c.request(ctx, http.MethodGet, "/v3/console/cs/config", nil)
		if err != nil {
			return "", err
		}
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("Nacos 配置读取失败（HTTP %d）", status)
	}
	return string(body), nil
}

func (c *nacosClient) login(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", c.opts.Username)
	form.Set("password", c.opts.Password)
	body, status, err := c.request(ctx, http.MethodPost, "/v3/auth/user/login", []byte(form.Encode()))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("Nacos 登录失败（HTTP %d）", status)
	}
	var payload struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return errors.New("Nacos 登录响应缺少 accessToken")
	}
	c.token = payload.AccessToken
	return nil
}

func (c *nacosClient) request(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	values := url.Values{}
	values.Set("dataId", c.opts.DataID)
	values.Set("groupName", c.opts.Group)
	if c.opts.Namespace != "" {
		values.Set("namespaceId", c.opts.Namespace)
	}
	target := c.baseURL() + path
	if method == http.MethodGet {
		target += "?" + values.Encode()
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(string(body)))
		if err != nil {
			return nil, 0, err
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
			req.Header.Set("accessToken", c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("Nacos 请求失败: %w", lastErr)
}

// parseNacosProperties 解析 properties 内容，只保留白名单键。
func parseNacosProperties(content string) map[string]string {
	values := make(map[string]string, len(nacosWhitelist))
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if !nacosWhitelist[key] {
			continue
		}
		value := strings.TrimSpace(line[idx+1:])
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			values[key] = value
		}
	}
	return values
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// RedactNacosMessage 用于日志：移除消息中可能出现的 DSN 与口令。
func RedactNacosMessage(message string, values map[string]string) string {
	out := message
	for _, value := range values {
		if value == "" {
			continue
		}
		out = strings.ReplaceAll(out, value, "<redacted>")
		if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				out = strings.ReplaceAll(out, password, "<redacted>")
			}
		}
	}
	return out
}

func getenvBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
