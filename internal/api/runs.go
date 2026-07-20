package api

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

type createRunReq struct {
	WorkflowID string          `json:"workflow_id"`
	Version    int32           `json:"version"` // 0=已发布版本 / -1=草稿调试 / >0=指定版本
	Input      json.RawMessage `json:"input"`
	Trigger    string          `json:"trigger"`
}

// createRun 落 pending 记录，后台 goroutine 调 engine.Run，立即返回 runID。
// 后台用独立 context，不随 HTTP 请求结束而取消（对齐 M9 异步契约：
// M9 只需把这段 goroutine 换成 asynq 入队，HTTP 契约不变）。
func (h *handlers) createRun(c *gin.Context) {
	var req createRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.WorkflowID == "" {
		failBadRequest(c, "workflow_id is required")
		return
	}

	runID, err := h.runs.CreateRun(c.Request.Context(), req.WorkflowID, req.Version, req.Input, req.Trigger)
	if err != nil {
		failFromErr(c, err)
		return
	}

	go func() {
		if err := h.engine.Run(context.Background(), runID); err != nil {
			h.logger.Warn("engine run finished with error",
				zap.String("run_id", runID), zap.Error(err))
		}
	}()

	httpx.OK(c, gin.H{"run_id": runID, "status": "pending"})
}

func (h *handlers) getRun(c *gin.Context) {
	view, err := h.runs.GetRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, view)
}

func (h *handlers) listRuns(c *gin.Context) {
	workflowID := c.Query("workflow_id")
	if workflowID == "" {
		failBadRequest(c, "workflow_id query param is required")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.runs.ListRuns(c.Request.Context(), workflowID, limit, offset)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}
