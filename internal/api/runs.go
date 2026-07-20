package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

type createRunReq struct {
	WorkflowID string          `json:"workflow_id"`
	Version    *int32          `json:"version"` // 省略=最新发布 / -1=草稿 / N>0=指定版本
	Input      json.RawMessage `json:"input"`
	Trigger    string          `json:"trigger"`
}

// createRun 落 pending 记录，交后台 dispatcher 调 engine.Run，立即返回 runID。
// dispatcher 用独立 context（不随 HTTP 请求取消），并提供 panic 兜底、并发上限、
// 优雅退出（对齐 M9 异步契约：M9 只需把 dispatcher.Submit 换成 asynq 入队）。
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

	h.logger.Info("create run: request received",
		zap.String("workflow_id", req.WorkflowID),
		zap.String("version_mode", versionMode(req.Version)),
		zap.String("trigger", req.Trigger),
	)

	runID, err := h.runs.CreateRun(c.Request.Context(), req.WorkflowID, req.Version, req.Input, req.Trigger)
	if err != nil {
		h.logger.Warn("create run: persist pending failed",
			zap.String("workflow_id", req.WorkflowID), zap.Error(err))
		failFromErr(c, err)
		return
	}
	h.logger.Info("create run: pending record created", zap.String("run_id", runID))

	// 满载 / 已关停：把刚落的 pending 记录改判 failed，回 503，避免僵尸 pending。
	if !h.runner.Submit(runID) {
		h.logger.Warn("create run: dispatcher rejected, marking failed", zap.String("run_id", runID))
		if ferr := h.engine.FailRunWithError(c.Request.Context(), runID, "run rejected: dispatcher at capacity or shutting down"); ferr != nil {
			h.logger.Error("failed to mark rejected run as failed", zap.String("run_id", runID), zap.Error(ferr))
		}
		httpx.Fail(c, http.StatusServiceUnavailable, "RUN_REJECTED",
			"server is at capacity, please retry later", gin.H{"run_id": runID})
		return
	}

	h.logger.Info("create run: submitted to dispatcher", zap.String("run_id", runID))
	httpx.OK(c, gin.H{"run_id": runID, "status": "pending"})
}

// versionMode 把 *int32 的 version 归一为可读的日志标签。
func versionMode(v *int32) string {
	if v == nil {
		return "latest_published"
	}
	switch {
	case *v == -1:
		return "draft"
	case *v > 0:
		return "explicit_v" + strconv.Itoa(int(*v))
	default:
		return "invalid"
	}
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
