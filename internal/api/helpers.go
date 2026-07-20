package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
	"github.com/smart-workflow/smart-workflow/internal/service"
)

// failFromErr 把 service 层错误映射为统一 HTTP 状态 + 错误码 envelope。
func failFromErr(c *gin.Context, err error) {
	// TD-10 run gate：校验失败带结构化 issues，422 + details.issues 供 Agent 自动修复。
	var verr *service.ValidationError
	if errors.As(err, &verr) {
		httpx.Fail(c, http.StatusUnprocessableEntity, "VALIDATION_FAILED", verr.Error(), gin.H{
			"issues": verr.Issues,
		})
		return
	}
	switch {
	case errors.Is(err, service.ErrNotFound), errors.Is(err, service.ErrNodeNotFound):
		httpx.Fail(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrVersionLock):
		httpx.Fail(c, http.StatusConflict, "VERSION_CONFLICT", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidVersion):
		httpx.Fail(c, http.StatusBadRequest, "INVALID_VERSION", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidJSON):
		httpx.Fail(c, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
	default:
		httpx.Fail(c, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
}

// failBadRequest 统一 400。
func failBadRequest(c *gin.Context, msg string) {
	httpx.Fail(c, http.StatusBadRequest, "BAD_REQUEST", msg, nil)
}

// pageParams 解析 ?limit=&offset=，带默认值与上限保护。
func pageParams(c *gin.Context) (limit, offset int32) {
	limit, offset = 20, 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = int32(n)
		}
	}
	if limit > 200 {
		limit = 200
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = int32(n)
		}
	}
	return limit, offset
}
