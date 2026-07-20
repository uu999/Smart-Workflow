package nodes

import (
	"context"
	"fmt"
	"strings"
)

// EndExecutor 收集输入并可选套用模板（对标 PaiFlow EndNodeExecutor）。
// nodeParam.template 支持 {{portName}} 占位符；无模板时直接透传输入。
type EndExecutor struct{}

func (EndExecutor) Type() string { return "end" }

func (EndExecutor) Execute(_ context.Context, ec *ExecContext) (*NodeResult, error) {
	out := make(map[string]any, len(ec.Inputs)+1)
	for k, v := range ec.Inputs {
		out[k] = v
	}

	if tpl, ok := ec.Node.Data.NodeParam["template"].(string); ok && tpl != "" {
		out["output"] = renderTemplate(tpl, ec.Inputs)
	}
	return &NodeResult{Outputs: out}, nil
}

// renderTemplate 把 {{key}} 替换为 inputs[key] 的字符串值。
func renderTemplate(tpl string, inputs map[string]any) string {
	result := tpl
	for k, v := range inputs {
		result = strings.ReplaceAll(result, "{{"+k+"}}", fmt.Sprintf("%v", v))
	}
	return result
}
