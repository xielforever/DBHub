package aiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/auth"
)

func TestOpenAIProtocolAndUnavailableState(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("provider request contract mismatch")
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body["model"] != "test-model" {
			t.Error("provider JSON mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"SELECT 1"}}]}`))
	}))
	defer upstream.Close()
	h := New(upstream.URL+"/v1", "test-key", "test-model")
	h.Client = upstream.Client()
	res, e := h.generate(context.Background(), Input{Prompt: "generate"})
	if e != nil || res != "SELECT 1" {
		t.Fatalf("real HTTPS protocol call failed: %v", e)
	}
	h.Client = &http.Client{}
	if _, e = h.generate(context.Background(), Input{Prompt: "x"}); e == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	h = New("https://api.example.com/v1", "", "model")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"prompt":"hello"}`))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsContextKey, &auth.Claims{Subject: 1}))
	h.Complete(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "尚未配置") {
		t.Fatal("unconfigured provider fabricated success")
	}
}
