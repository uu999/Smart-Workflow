package api

import (
	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

type createWorkflowReq struct {
	ProjectID   string   `json:"project_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Draft       *dsl.DSL `json:"draft"`
}

func (h *handlers) createWorkflow(c *gin.Context) {
	var req createWorkflowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.ProjectID == "" || req.Name == "" {
		failBadRequest(c, "project_id and name are required")
		return
	}
	id, err := h.workflows.Create(c.Request.Context(), req.ProjectID, req.Name, req.Description, req.Draft)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"workflow_id": id})
}

func (h *handlers) getWorkflow(c *gin.Context) {
	wf, err := h.workflows.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, wf)
}

func (h *handlers) listWorkflows(c *gin.Context) {
	projectID := c.Query("project_id")
	if projectID == "" {
		failBadRequest(c, "project_id query param is required")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.workflows.List(c.Request.Context(), projectID, limit, offset)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type updateWorkflowReq struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Draft       *dsl.DSL `json:"draft"`
	VersionLock int32    `json:"version_lock"`
}

func (h *handlers) updateWorkflow(c *gin.Context) {
	var req updateWorkflowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Name == "" {
		failBadRequest(c, "name is required")
		return
	}
	if err := h.workflows.UpdateDraft(c.Request.Context(), c.Param("id"), req.Name, req.Description, req.Draft, req.VersionLock); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"workflow_id": c.Param("id")})
}

func (h *handlers) deleteWorkflow(c *gin.Context) {
	if err := h.workflows.Delete(c.Request.Context(), c.Param("id")); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

type publishWorkflowReq struct {
	ChangeLog string `json:"change_log"`
}

func (h *handlers) publishWorkflow(c *gin.Context) {
	var req publishWorkflowReq
	_ = c.ShouldBindJSON(&req) // change_log 可空
	ver, err := h.workflows.Publish(c.Request.Context(), c.Param("id"), req.ChangeLog)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"workflow_id": c.Param("id"), "version": ver})
}

func (h *handlers) validateWorkflow(c *gin.Context) {
	result, err := h.validate.ValidateDraft(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{
		"has_error": result.HasError(),
		"issues":    result.Issues,
	})
}

type nodeDebugReq struct {
	NodeID        string         `json:"node_id"`
	Inputs        map[string]any `json:"inputs"`
	CostTargetSec float64        `json:"cost_target_sec"`
}

func (h *handlers) nodeDebugWorkflow(c *gin.Context) {
	var req nodeDebugReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.NodeID == "" {
		failBadRequest(c, "node_id is required")
		return
	}
	result, err := h.nodeDebug.DebugNode(c.Request.Context(), c.Param("id"), req.NodeID, req.Inputs, req.CostTargetSec)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, result)
}
