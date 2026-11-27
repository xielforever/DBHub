package config

import "testing"

func TestDecodeConfigResponse(t *testing.T) {
	wrapped, err := decodeConfigResponse([]byte(`{"code":0,"message":"success","data":{"content":"DATABASE_URL=x\n"}}`))
	if err != nil || wrapped != "DATABASE_URL=x\n" {
		t.Fatalf("wrapped: %q err=%v", wrapped, err)
	}
	raw, err := decodeConfigResponse([]byte("DATABASE_URL=y\n"))
	if err != nil || raw != "DATABASE_URL=y\n" {
		t.Fatalf("raw: %q err=%v", raw, err)
	}
	if _, err := decodeConfigResponse([]byte(`{"code":100,"message":"denied","data":{}}`)); err == nil {
		t.Fatal("非零 code 应返回错误")
	}
	if _, err := decodeConfigResponse([]byte(`{"code":0,"data":{"content":""}}`)); err == nil {
		t.Fatal("空内容应返回错误")
	}
}
