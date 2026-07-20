package engine

import (
	"context"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
)

// Assertion 是单节点调试的一条断言结果（新增，评测信号）。
type Assertion struct {
	Type   string  `json:"type"`
	Target float64 `json:"target,omitempty"`
	Pass   bool    `json:"pass"`
}

// DebugResult 对齐设计文档 §4（抄 PaiFlow NodeDebugRespVo + 断言层）。
type DebugResult struct {
	NodeID      string         `json:"node_id"`
	NodeType    string         `json:"node_type"`
	Status      string         `json:"status"`
	Input       map[string]any `json:"input"`
	Output      map[string]any `json:"output,omitempty"`
	ExecCostSec float64        `json:"exec_cost_sec"`
	Error       string         `json:"error,omitempty"`
	Assertions  []Assertion    `json:"assertions"`
}

// DebugNode 单节点隔离调试：调用方直接喂 inputs（不做变量池上游解析），
// 强制关闭重试（should_retry=false）以拿即时真实结果（抄 PaiFlow node_debug）。
// costTargetSec > 0 时追加 cost_under_sec 断言。
func (e *Engine) DebugNode(ctx context.Context, node dsl.DSLNode, inputs map[string]any, costTargetSec float64) *DebugResult {
	nodeType := node.Data.NodeMeta.NodeType
	res := &DebugResult{
		NodeID:   node.ID,
		NodeType: nodeType,
		Input:    inputs,
	}

	exec, ok := e.registry.Get(nodeType)
	if !ok {
		res.Status = nodes.StatusFailed
		res.Error = "no executor registered for node type " + nodeType
		res.Assertions = buildAssertions(res, costTargetSec)
		return res
	}

	// 关重试：拿即时真实结果，不被重试掩盖问题。
	node.Data.RetryConfig.ShouldRetry = false
	node.Data.RetryConfig.MaxRetries = 0

	ec := &nodes.ExecContext{
		Node:   node,
		Inputs: inputs,
		Pool:   varpool.New(),
		RunID:  "debug",
	}

	start := time.Now()
	out, err := exec.Execute(ctx, ec)
	res.ExecCostSec = time.Since(start).Seconds()

	if err != nil {
		res.Status = nodes.StatusFailed
		res.Error = err.Error()
	} else {
		res.Status = nodes.StatusSucceeded
		res.Output = out.Outputs
	}
	res.Assertions = buildAssertions(res, costTargetSec)
	return res
}

// buildAssertions 生成 MVP 三条断言：status_success / output_not_empty / cost_under_sec。
func buildAssertions(r *DebugResult, costTargetSec float64) []Assertion {
	assertions := []Assertion{
		{Type: "status_success", Pass: r.Status == nodes.StatusSucceeded},
		{Type: "output_not_empty", Pass: len(r.Output) > 0},
	}
	if costTargetSec > 0 {
		assertions = append(assertions, Assertion{
			Type:   "cost_under_sec",
			Target: costTargetSec,
			Pass:   r.ExecCostSec <= costTargetSec,
		})
	}
	return assertions
}
