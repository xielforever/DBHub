package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/user/dbhub/internal/queryapi"
)

type apiClient struct {
	base, admin string
	http        *http.Client
}
type apiResult struct {
	status, code, bytes int
	data                json.RawMessage
	elapsed             time.Duration
	err                 error
}

func newClient(base string) *apiClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 128
	return &apiClient{base: strings.TrimRight(base, "/"), http: &http.Client{Transport: transport, Timeout: 45 * time.Second}}
}
func (c *apiClient) call(ctx context.Context, method, path, token string, body any) apiResult {
	started := time.Now()
	raw, err := json.Marshal(body)
	if err != nil {
		return apiResult{err: fmt.Errorf("请求编码失败")}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return apiResult{err: fmt.Errorf("请求配置无效")}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return apiResult{elapsed: time.Since(started), err: fmt.Errorf("请求未获得响应")}
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return apiResult{status: res.StatusCode, elapsed: time.Since(started), err: fmt.Errorf("读取响应失败")}
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return apiResult{status: res.StatusCode, elapsed: time.Since(started), err: fmt.Errorf("响应格式无效")}
	}
	return apiResult{status: res.StatusCode, code: envelope.Code, data: envelope.Data, bytes: len(raw), elapsed: time.Since(started)}
}
func (c *apiClient) control(ctx context.Context, method, path string, body, out any) error {
	r := c.call(ctx, method, "/api/v1"+path, c.admin, body)
	if r.err != nil || r.status < 200 || r.status >= 300 || r.code != 0 {
		return fmt.Errorf("控制请求失败：%s %s（HTTP %d）", method, path, r.status)
	}
	if out != nil && json.Unmarshal(r.data, out) != nil {
		return fmt.Errorf("控制响应格式错误：%s", path)
	}
	return nil
}
func (c *apiClient) login(ctx context.Context, username, password string) (string, error) {
	r := c.call(ctx, "POST", "/api/v1/auth/login", "", map[string]any{"username": username, "password": password})
	var data struct {
		Token string `json:"access_token"`
	}
	if r.err != nil || r.status != 200 || r.code != 0 || json.Unmarshal(r.data, &data) != nil || data.Token == "" {
		return "", fmt.Errorf("压测账号登录失败（HTTP %d）", r.status)
	}
	return data.Token, nil
}
func (c *apiClient) metrics(ctx context.Context) (queryapi.QueryMetricsSnapshot, error) {
	var s queryapi.QueryMetricsSnapshot
	err := c.control(ctx, "GET", "/metrics/query", nil, &s)
	if err == nil && (!s.ReceiptStoreAvailable || s.StartedAt.IsZero()) {
		err = fmt.Errorf("运行指标或回执存储不可用")
	}
	return s, err
}
func (c *apiClient) idle(ctx context.Context) (queryapi.QueryMetricsSnapshot, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := c.metrics(ctx)
		if err != nil {
			return s, err
		}
		if s.Requests.InFlight == 0 && s.Executions.InFlight == 0 && s.Queue.Running == 0 && s.Queue.Waiting == 0 {
			return s, nil
		}
		if time.Now().After(deadline) {
			return s, fmt.Errorf("服务仍有执行任务，无法建立安静的压测边界")
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("压测已取消")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("安全随机数不可用")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func nonce() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("安全随机数不可用")
	}
	return hex.EncodeToString(b[:])
}
