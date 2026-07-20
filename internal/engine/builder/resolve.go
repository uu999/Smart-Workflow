package builder

import (
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/engine/varpool"
)

// ResolveInputs 把节点内联的输入绑定解析成具体值（供调度器在执行前调用）。
//   - ref：从变量池按 RefContent.NodeID + Name(路径) 取值
//   - literal：直接取 Content
//   - 空 Value（clear/未绑定）：跳过
//
// start 节点无上游绑定，其输入由运行入参注入，不走这里。
func ResolveInputs(node dsl.DSLNode, pool *varpool.VariablePool) (map[string]any, error) {
	out := make(map[string]any, len(node.Data.Inputs))
	for _, in := range node.Data.Inputs {
		v := in.Schema.Value
		switch v.Type {
		case dsl.DSLValueRef:
			rc, ok := refContentOf(v.Content)
			if !ok || rc.NodeID == "" {
				return nil, fmt.Errorf("node %s input %s: invalid ref content", node.ID, in.Name)
			}
			val, err := pool.Resolve(rc.NodeID, rc.Name)
			if err != nil {
				return nil, fmt.Errorf("node %s input %s: %w", node.ID, in.Name, err)
			}
			out[in.Name] = val
		case dsl.DSLValueLiteral:
			out[in.Name] = v.Content
		default:
			// 空类型 = 未绑定 / clear，跳过。
		}
	}
	return out, nil
}
