package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Envelope 是所有 API / CLI 的统一响应体，对齐设计文档 §3。
//
//	成功: { "ok": true,  "data": {...} }
//	失败: { "ok": false, "error": { "code": "...", "message": "...", "details": {...} } }
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// Error 是结构化错误，code 供机器判断，message 供人读，
// details 里可放 hint / available_options 等，供 Agent 自动修复。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// OK 写一个成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{OK: true, Data: data})
}

// Fail 写一个失败响应。httpStatus 为 HTTP 状态码。
func Fail(c *gin.Context, httpStatus int, code, message string, details any) {
	c.JSON(httpStatus, Envelope{
		OK:    false,
		Error: &Error{Code: code, Message: message, Details: details},
	})
}
