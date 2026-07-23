package nodes

import (
	"context"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// DatasetExecutor 是 dataset 节点执行器（M10）：按节点的 dataset_id 经 DatasetResolver
// 取回评测集行集，输出 {rows: [...], count: n}，供下游（分类器 batch 迭代源）消费。
//
// 不直接依赖 DB——Resolver 由 nodes.Config 注入（engine 装配时以 DatasetService 适配）。
type DatasetExecutor struct {
	Resolver DatasetResolver
}

func (DatasetExecutor) Type() string { return dsl.KindDataset }

func (e DatasetExecutor) Execute(ctx context.Context, ec *ExecContext) (*NodeResult, error) {
	if e.Resolver == nil {
		return nil, fmt.Errorf("dataset node %s: no dataset resolver configured", ec.Node.ID)
	}
	datasetID := datasetIDOf(ec.Node)
	if datasetID == "" {
		return nil, fmt.Errorf("dataset node %s: missing dataset_id", ec.Node.ID)
	}
	rows, err := e.Resolver.ResolveDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("dataset node %s: resolve %s: %w", ec.Node.ID, datasetID, err)
	}
	// rows 以 []any 输出，便于下游 batch 迭代与 varpool 存取（统一 any 元素）。
	items := make([]any, len(rows))
	for i, r := range rows {
		items[i] = r
	}
	return &NodeResult{Outputs: map[string]any{
		"rows":  items,
		"count": len(items),
	}}, nil
}

// datasetIDOf 从 DSL 节点取 dataset_id：优先 nodeParam.datasetId（render 物化），
// 兼容 dataset_id 下划线写法。
func datasetIDOf(n dsl.DSLNode) string {
	p := n.Data.NodeParam
	if v, ok := p["datasetId"].(string); ok && v != "" {
		return v
	}
	if v, ok := p["dataset_id"].(string); ok && v != "" {
		return v
	}
	return ""
}
