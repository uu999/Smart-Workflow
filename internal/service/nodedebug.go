package service

import (
	"context"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

// ErrNodeNotFound 表示工作流里找不到指定节点。
var ErrNodeNotFound = fmt.Errorf("node not found in workflow")

// NodeDebugService 编排单节点调试（M6）：定位节点 → 交给 engine 执行 + 断言。
type NodeDebugService struct {
	store  *mysql.Store
	wf     *WorkflowService
	engine *engine.Engine
}

func NewNodeDebugService(store *mysql.Store, eng *engine.Engine) *NodeDebugService {
	return &NodeDebugService{store: store, wf: NewWorkflowService(store), engine: eng}
}

// DebugNode 在工作流草稿里定位 nodeID，用调用方提供的 inputs 隔离执行该节点。
// costTargetSec > 0 时追加 cost_under_sec 断言。
func (s *NodeDebugService) DebugNode(ctx context.Context, workflowID, nodeID string, inputs map[string]any, costTargetSec float64) (*engine.DebugResult, error) {
	wf, err := s.wf.Get(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	node, ok := findNode(wf.Draft, nodeID)
	if !ok {
		return nil, ErrNodeNotFound
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	return s.engine.DebugNode(ctx, node, inputs, costTargetSec), nil
}

// findNode 在 DSL 里按 ID 找节点。
func findNode(d *dsl.DSL, nodeID string) (dsl.DSLNode, bool) {
	if d == nil {
		return dsl.DSLNode{}, false
	}
	for _, n := range d.Nodes {
		if n.ID == nodeID {
			return n, true
		}
	}
	return dsl.DSLNode{}, false
}
