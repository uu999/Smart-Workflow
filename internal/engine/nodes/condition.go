package nodes

import (
	"context"
	"fmt"
	"strings"
)

// ConditionExecutor 按分支表达式选择命中的分支。
// nodeParam.branches 形如：
//
//	[ { "index": 0, "conditions": [ {"left_port":"score","comparator":"gte","right":0.8} ] }, ... ]
//
// 一个分支内的多个 condition 取 AND；按 index 顺序取第一个全真的分支。
// 命中返回 NodeResult.Branch = 分支序号字符串；无命中返回 "default"。
type ConditionExecutor struct{}

func (ConditionExecutor) Type() string { return "condition" }

func (ConditionExecutor) Execute(_ context.Context, ec *ExecContext) (*NodeResult, error) {
	branches, ok := ec.Node.Data.NodeParam["branches"].([]any)
	if !ok || len(branches) == 0 {
		return nil, fmt.Errorf("condition node %s has no branches", ec.Node.ID)
	}

	for _, raw := range branches {
		br, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		conds, _ := br["conditions"].([]any)
		if allConditionsTrue(conds, ec.Inputs) {
			idx := fmt.Sprintf("%v", br["index"])
			return &NodeResult{
				Outputs: map[string]any{"matched_branch": idx},
				Branch:  idx,
			}, nil
		}
	}
	return &NodeResult{Outputs: map[string]any{"matched_branch": "default"}, Branch: "default"}, nil
}

func allConditionsTrue(conds []any, inputs map[string]any) bool {
	if len(conds) == 0 {
		return false
	}
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			return false
		}
		port, _ := cm["left_port"].(string)
		comparator, _ := cm["comparator"].(string)
		left := inputs[port]
		right := cm["right"]
		if !compare(left, comparator, right) {
			return false
		}
	}
	return true
}

// compare 支持 eq/ne/gt/gte/lt/lte/contains。数值比较统一转 float64。
func compare(left any, comparator string, right any) bool {
	switch comparator {
	case "eq":
		return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
	case "ne":
		return fmt.Sprintf("%v", left) != fmt.Sprintf("%v", right)
	case "gt", "gte", "lt", "lte":
		lf, lok := toFloat(left)
		rf, rok := toFloat(right)
		if !lok || !rok {
			return false
		}
		switch comparator {
		case "gt":
			return lf > rf
		case "gte":
			return lf >= rf
		case "lt":
			return lf < rf
		case "lte":
			return lf <= rf
		}
	case "contains":
		return fmt.Sprintf("%v", right) != "" &&
			strings.Contains(fmt.Sprintf("%v", left), fmt.Sprintf("%v", right))
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
