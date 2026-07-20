package varpool

import (
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
)

// ValidateOutputs 按节点声明的 outputs 校验实际输出值的类型与必填
// （对标 PaiFlow VariablePool.do_validate 的轻量版）。
// 返回所有问题；为空表示通过。
func ValidateOutputs(outputs map[string]any, decls []dsl.Port) []string {
	var problems []string
	for _, d := range decls {
		v, ok := outputs[d.Name]
		if !ok {
			if d.Required {
				problems = append(problems, fmt.Sprintf("missing required output %q", d.Name))
			}
			continue
		}
		if d.Type != "" && !typeMatches(d.Type, v) {
			problems = append(problems, fmt.Sprintf("output %q type mismatch: want %s, got %T", d.Name, d.Type, v))
		}
	}
	return problems
}

// typeMatches 判断值 v 是否符合声明类型 t。
// 采用 JSON 反序列化后的通用形态：数字统一为 float64。
func typeMatches(t string, v any) bool {
	if v == nil {
		return true // null 视为可接受，交由 required 逻辑处理
	}
	switch t {
	case dsl.ValueTypeString:
		_, ok := v.(string)
		return ok
	case dsl.ValueTypeBoolean:
		_, ok := v.(bool)
		return ok
	case dsl.ValueTypeInteger:
		return isInteger(v)
	case dsl.ValueTypeNumber:
		return isNumber(v)
	case dsl.ValueTypeArray:
		_, ok := v.([]any)
		return ok
	case dsl.ValueTypeObject:
		_, ok := v.(map[string]any)
		return ok
	default:
		return true // 未知类型不拦
	}
}

func isNumber(v any) bool {
	switch v.(type) {
	case float64, float32, int, int32, int64:
		return true
	default:
		return false
	}
}

func isInteger(v any) bool {
	switch n := v.(type) {
	case int, int32, int64:
		return true
	case float64:
		return n == float64(int64(n)) // JSON 数字统一 float64，判断是否整数
	case float32:
		return n == float32(int32(n))
	default:
		return false
	}
}
