package api

import (
	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

type createProjectReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *handlers) createProject(c *gin.Context) {
	var req createProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Name == "" {
		failBadRequest(c, "name is required")
		return
	}
	id, err := h.projects.Create(c.Request.Context(), req.Name, req.Description)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"project_id": id})
}

func (h *handlers) getProject(c *gin.Context) {
	p, err := h.projects.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, p)
}

func (h *handlers) listProjects(c *gin.Context) {
	limit, offset := pageParams(c)
	items, err := h.projects.List(c.Request.Context(), limit, offset)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

func (h *handlers) updateProject(c *gin.Context) {
	var req createProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Name == "" {
		failBadRequest(c, "name is required")
		return
	}
	if err := h.projects.Update(c.Request.Context(), c.Param("id"), req.Name, req.Description); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"project_id": c.Param("id")})
}

func (h *handlers) deleteProject(c *gin.Context) {
	if err := h.projects.Delete(c.Request.Context(), c.Param("id")); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}
