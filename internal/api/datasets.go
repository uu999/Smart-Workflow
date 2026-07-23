package api

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

// datasets 端点（M10 #36）：补齐评测集的接入层。存储/执行器早已就绪
// （DatasetService + DatasetExecutor），此前只缺 HTTP 入口，导致样例无法灌数据。
// 契约对齐 applications.go：rows 必须是 JSON 数组，row_count 由服务端派生。

type createDatasetReq struct {
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Schema    json.RawMessage `json:"schema"`
	Rows      json.RawMessage `json:"rows"`
}

func (h *handlers) createDataset(c *gin.Context) {
	var req createDatasetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.ProjectID == "" || req.Name == "" {
		failBadRequest(c, "project_id and name are required")
		return
	}
	id, err := h.datasets.Create(c.Request.Context(), req.ProjectID, req.Name, req.Schema, req.Rows)
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"dataset_id": id})
}

func (h *handlers) getDataset(c *gin.Context) {
	ds, err := h.datasets.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, ds)
}

func (h *handlers) listDatasets(c *gin.Context) {
	projectID := c.Query("project_id")
	if projectID == "" {
		failBadRequest(c, "project_id query param is required")
		return
	}
	limit, offset := pageParams(c)
	// ?name= 存在时走模糊搜索（对齐 applications 列表语义）；否则普通分页。
	name, searching := c.GetQuery("name")
	var (
		items any
		err   error
	)
	if searching {
		items, err = h.datasets.Search(c.Request.Context(), projectID, name, limit, offset)
	} else {
		items, err = h.datasets.List(c.Request.Context(), projectID, limit, offset)
	}
	if err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type updateDatasetReq struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Rows   json.RawMessage `json:"rows"`
}

func (h *handlers) updateDataset(c *gin.Context) {
	var req updateDatasetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Name == "" {
		failBadRequest(c, "name is required")
		return
	}
	if err := h.datasets.Update(c.Request.Context(), c.Param("id"), req.Name, req.Schema, req.Rows); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"dataset_id": c.Param("id")})
}

func (h *handlers) deleteDataset(c *gin.Context) {
	if err := h.datasets.Delete(c.Request.Context(), c.Param("id")); err != nil {
		failFromErr(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}
