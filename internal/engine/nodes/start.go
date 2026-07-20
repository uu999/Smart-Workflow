package nodes

import "context"

// StartExecutor 把输入原样作为输出（对标 PaiFlow StartNodeExecutor）。
// 运行时引擎会把工作流入参作为 start 节点的 Inputs 注入。
type StartExecutor struct{}

func (StartExecutor) Type() string { return "start" }

func (StartExecutor) Execute(_ context.Context, ec *ExecContext) (*NodeResult, error) {
	out := make(map[string]any, len(ec.Inputs))
	for k, v := range ec.Inputs {
		out[k] = v
	}
	return &NodeResult{Outputs: out}, nil
}
