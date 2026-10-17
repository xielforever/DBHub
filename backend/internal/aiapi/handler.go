// Package aiapi forwards explicit user requests to an OpenAI-compatible HTTPS API.
package aiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
	mu                     sync.Mutex
	active                 map[int64]bool
}

func New(base, key, model string) *Handler {
	return &Handler{BaseURL: strings.TrimRight(base, "/"), APIKey: key, Model: model, Client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, active: map[int64]bool{}}
}
func (h *Handler) configured() bool {
	u, e := url.Parse(h.BaseURL)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && h.APIKey != "" && h.Model != ""
}
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{"configured": h.configured(), "model": h.Model})
}

type Input struct {
	Prompt  string   `json:"prompt"`
	SQL     string   `json:"sql"`
	Dialect string   `json:"dialect"`
	Tables  []string `json:"tables"`
	Mode    string   `json:"mode"`
	Explain string   `json:"explain"`
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		httpx.JSON(w, 503, httpx.Envelope{Code: 50301, Message: "模型服务尚未配置，请在云环境设置 AI 服务后重试"})
		return
	}
	var in Input
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if len(in.Prompt)+len(in.SQL)+len(in.Explain) > 24000 || len(in.Tables) > 100 || strings.TrimSpace(in.Prompt) == "" {
		httpx.Fail(w, httpx.BadRequest("请输入问题（总上下文不超过 24000 字节）"))
		return
	}
	c, _ := auth.ClaimsFromContext(r.Context())
	h.mu.Lock()
	if h.active[c.Subject] {
		h.mu.Unlock()
		httpx.Fail(w, httpx.New(42900, "已有 AI 请求执行中", nil))
		return
	}
	h.active[c.Subject] = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.active, c.Subject); h.mu.Unlock() }()
	answer, e := h.generate(r.Context(), in)
	if e != nil {
		httpx.JSON(w, 502, httpx.Envelope{Code: 50201, Message: e.Error()})
		return
	}
	httpx.OK(w, map[string]string{"content": answer, "model": h.Model})
}
func (h *Handler) generate(ctx context.Context, in Input) (string, error) {
	contextJSON, _ := json.Marshal(in)
	body, _ := json.Marshal(map[string]any{"model": h.Model, "stream": false, "max_tokens": 2000, "messages": []map[string]string{{"role": "system", "content": "你是 SQL 助手。使用简体中文，仅依据用户提交的数据库类型、SQL、表名和 EXPLAIN 提供建议。未知信息应说明；不得编造执行耗时或优化收益。SQL 放入 sql 代码块。所有输出仅供用户审阅，不会自动执行。"}, {"role": "user", "content": string(contextJSON)}}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/chat/completions", bytes.NewReader(body))
	if e != nil {
		return "", fmt.Errorf("模型请求构造失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	res, e := h.Client.Do(req)
	if e != nil {
		return "", fmt.Errorf("模型服务连接失败或超时")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("模型服务返回 HTTP %d，请检查服务配置", res.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); e != nil || len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("模型服务返回内容无效")
	}
	return out.Choices[0].Message.Content, nil
}
