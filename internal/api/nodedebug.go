package api

import (
	"github.com/gin-gonic/gin"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/httpx"
)

// statelessNodeDebugReq 是无状态单节点调试请求（风险1）：
// CLI 在本地把 IR 渲染成 DSL，取出要调的那个 DSL 节点直接提交，
// 不需要先 upload 成 workflow 再按 id 调试——支撑"upload 前先验证"闭环。
type statelessNodeDebugReq struct {
	Node          dsl.DSLNode    `json:"node"`
	Inputs        map[string]any `json:"inputs"`
	CostTargetSec float64        `json:"cost_target_sec"`
}

// debugNode 是不依赖存储的单节点调试端点：直接吃渲染后的 DSL 节点 + inputs，
// 复用 engine.DebugNode（关重试 + 断言）。sidecar 访问仍集中在服务端，CLI 保持薄。
func (h *handlers) debugNode(c *gin.Context) {
	var req statelessNodeDebugReq
	if err := c.ShouldBindJSON(&req); err != nil {
		failBadRequest(c, err.Error())
		return
	}
	if req.Node.ID == "" {
		failBadRequest(c, "node.id is required")
		return
	}
	if req.Node.Data.NodeMeta.NodeType == "" {
		failBadRequest(c, "node.data.nodeMeta.nodeType is required")
		return
	}
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}
	result := h.engine.DebugNode(c.Request.Context(), req.Node, req.Inputs, req.CostTargetSec)
	httpx.OK(c, result)
}
