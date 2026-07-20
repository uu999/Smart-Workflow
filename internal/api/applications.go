package api

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

type createApplicationReq struct {
	ProjectID    string          `json:"project_id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"` // http/python/rpc
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Config       json.RawMessage `json:"config"`
}

func (h *handlers) createApplication(c *gin.Context) {
	var req createApplicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.ProjectID == "" || req.Name == "" || req.Kind == "" {
		failBadRequest(c, "project_id, name and kind are required")
		return
	}
	id, err := h.apps.Create(c.Request.Context(), req.ProjectID, req.Name, req.Kind, req.InputSchema, req.OutputSchema, req.Config)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"app_id": id})
}

func (h *handlers) getApplication(c *gin.Context) {
	app, err := h.apps.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, app)
}

func (h *handlers) listApplications(c *gin.Context) {
	projectID := c.Query("project_id")
	if projectID == "" {
		failBadRequest(c, "project_id query param is required")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.apps.List(c.Request.Context(), projectID, limit, offset)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type updateApplicationReq struct {
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Config       json.RawMessage `json:"config"`
}

func (h *handlers) updateApplication(c *gin.Context) {
	var req updateApplicationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Name == "" || req.Kind == "" {
		failBadRequest(c, "name and kind are required")
		return
	}
	if err := h.apps.Update(c.Request.Context(), c.Param("id"), req.Name, req.Kind, req.InputSchema, req.OutputSchema, req.Config); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"app_id": c.Param("id")})
}

func (h *handlers) deleteApplication(c *gin.Context) {
	if err := h.apps.Delete(c.Request.Context(), c.Param("id")); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}
