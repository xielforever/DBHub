// Package httpx 提供统一响应封装与应用错误类型。
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// 业务错误码（HTTP 状态码 * 100 + 子码）。
const (
	CodeOK           = 0
	CodeBadRequest   = 40000
	CodeUnauthorized = 40100
	CodeForbidden    = 40300
	CodeNotFound     = 40400
	CodeConflict     = 40900
	CodeInternal     = 50000
)

// AppError 业务错误统一结构，Err 为内部错误，不会序列化暴露给前端。
type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap 支持 errors.Is / errors.As 解包。
func (e *AppError) Unwrap() error {
	return e.Err
}

// New 构造业务错误。
func New(code int, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

// BadRequest 400 参数错误。
func BadRequest(message string) *AppError {
	return &AppError{Code: CodeBadRequest, Message: message}
}

// Unauthorized 401 未认证。
func Unauthorized(message string) *AppError {
	return &AppError{Code: CodeUnauthorized, Message: message}
}

// Forbidden 403 已认证但无权限。
func Forbidden(message string) *AppError {
	return &AppError{Code: CodeForbidden, Message: message}
}

// Conflict 409 资源冲突。
func Conflict(message string) *AppError {
	return &AppError{Code: CodeConflict, Message: message}
}

// NotFound 404 资源不存在。
func NotFound(message string) *AppError {
	return &AppError{Code: CodeNotFound, Message: message}
}

// Internal 500 系统错误。
func Internal(message string, err error) *AppError {
	return &AppError{Code: CodeInternal, Message: message, Err: err}
}

// Envelope 统一响应信封：{code, message, data}。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON 写出 JSON 响应。
func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("响应编码失败", "error", err)
	}
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Envelope{Code: CodeOK, Message: "ok", Data: data})
}

// Fail 根据错误类型写出失败响应；内部错误仅返回通用提示，细节写日志。
func Fail(w http.ResponseWriter, err error) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		appErr = Internal("服务器内部错误", err)
	}

	status := httpStatusForCode(appErr.Code)
	if status >= 500 {
		slog.Error("请求处理失败", "code", appErr.Code, "error", appErr.Err)
		// 绝不向前端暴露内部错误堆栈
		JSON(w, status, Envelope{Code: appErr.Code, Message: "服务器内部错误，请稍后重试"})
		return
	}

	slog.Warn("业务请求被拒绝", "code", appErr.Code, "message", appErr.Message)
	JSON(w, status, Envelope{Code: appErr.Code, Message: appErr.Message})
}

// DecodeJSON 以大小受限、字段严格的方式解析请求体。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest("请求参数格式错误: " + err.Error())
	}
	return nil
}

func httpStatusForCode(code int) int {
	switch {
	case code >= 50000:
		return http.StatusInternalServerError
	case code >= 40900 && code < 41000:
		return http.StatusConflict
	case code >= 40400 && code < 40500:
		return http.StatusNotFound
	case code >= 40300 && code < 40400:
		return http.StatusForbidden
	case code >= 40100 && code < 40200:
		return http.StatusUnauthorized
	case code >= 40000 && code < 40100:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
