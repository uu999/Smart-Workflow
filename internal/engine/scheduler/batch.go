package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/nodes"
)

// batchSpec 是从 DSL 节点 nodeParam["batch"] 解析出的批处理配置。
// 对齐 render 写出的字段（enable/source_node/source_port/item_name/size）。
type batchSpec struct {
	enable   bool
	itemName string // 迭代变量名（默认 = source_port）；执行时替换 inputs[itemName] 为单个 item
	size     int    // >0 时限制处理条数（截断），<=0 不限
}

// batchOf 解析节点的 batch 配置；未开启返回 (spec{}, false)。
func batchOf(node dsl.DSLNode) (batchSpec, bool) {
	raw, ok := node.Data.NodeParam["batch"].(map[string]any)
	if !ok {
		return batchSpec{}, false
	}
	enable, _ := raw["enable"].(bool)
	if !enable {
		return batchSpec{}, false
	}
	spec := batchSpec{enable: true}
	if v, ok := raw["item_name"].(string); ok {
		spec.itemName = v
	}
	if spec.itemName == "" {
		if v, ok := raw["source_port"].(string); ok {
			spec.itemName = v
		}
	}
	if v, ok := toInt(raw["size"]); ok {
		spec.size = v
	}
	return spec, spec.itemName != ""
}

// runNode 执行单节点，若开启 batch 则对迭代源数组逐项执行并聚合。
// batch 是「包在任意执行器外的通用 map 包装」（plan.md 纠正2）：调度器 DAG 与
// node_run 1:1 不变，聚合结果作为该节点的单条输出（{items:[...], count:n}）。
func (c *coord) runNode(ctx context.Context, exec nodes.NodeExecutor, ec *nodes.ExecContext, cfg dsl.RetryConfig) (*nodes.NodeResult, time.Duration, int, error) {
	spec, on := batchOf(ec.Node)
	if !on {
		return runWithRetry(ctx, exec, ec, cfg)
	}

	src, ok := ec.Inputs[spec.itemName]
	if !ok {
		return nil, 0, 0, fmt.Errorf("batch node %s: iterate source input %q not found", ec.Node.ID, spec.itemName)
	}
	list, ok := toList(src)
	if !ok {
		return nil, 0, 0, fmt.Errorf("batch node %s: iterate source %q is not an array", ec.Node.ID, spec.itemName)
	}
	if spec.size > 0 && len(list) > spec.size {
		list = list[:spec.size]
	}

	start := time.Now()
	items := make([]any, 0, len(list))
	totalAttempts := 0
	for i, item := range list {
		// 每次迭代：复制 inputs，把迭代变量替换为单个 item。
		iterInputs := make(map[string]any, len(ec.Inputs))
		for k, v := range ec.Inputs {
			iterInputs[k] = v
		}
		iterInputs[spec.itemName] = item

		iterCtx := &nodes.ExecContext{Node: ec.Node, Inputs: iterInputs, Pool: ec.Pool, RunID: ec.RunID}
		res, _, attempt, err := runWithRetry(ctx, exec, iterCtx, cfg)
		totalAttempts += attempt
		if err != nil {
			return nil, time.Since(start), totalAttempts, fmt.Errorf("batch node %s item %d: %w", ec.Node.ID, i, err)
		}
		items = append(items, res.Outputs)
	}
	return &nodes.NodeResult{Outputs: map[string]any{
		"items": items,
		"count": len(items),
	}}, time.Since(start), totalAttempts, nil
}

// toList 把 any 归一为 []any（支持 []any 与 []map[string]any 等常见形态）。
func toList(v any) ([]any, bool) {
	switch arr := v.(type) {
	case []any:
		return arr, true
	case []map[string]any:
		out := make([]any, len(arr))
		for i, m := range arr {
			out[i] = m
		}
		return out, true
	default:
		return nil, false
	}
}

// toInt 宽松地把 JSON 数值（float64/int）转 int。
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
