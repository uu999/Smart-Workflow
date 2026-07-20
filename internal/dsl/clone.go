package dsl

import (
	"fmt"
	"strings"
)

// ToIR 把执行态 DSL 反渲染回编辑态 IR（设计文档 §10.5 逆操作），
// 供 clone-ref "复用优先" 策略使用：
// 抽 name 丢 uuid、内联 value 提成扁平 bindings、type::uuid 换回可读 ID。
//
// meta 由调用方提供（DSL 本身不含 meta）；source 记录来源工作流。
func ToIR(d *DSL, meta Meta) (*IR, error) {
	// 建立 dsl.id -> 可读 id 映射：按 kind 计数（application_0, application_1 ...）。
	idMap := make(map[string]string, len(d.Nodes))
	counter := make(map[string]int)
	for _, n := range d.Nodes {
		kind := nodeTypeOf(n)
		readable := fmt.Sprintf("%s_%d", kind, counter[kind])
		counter[kind]++
		idMap[n.ID] = readable
	}

	ir := &IR{
		Meta:  meta,
		Nodes: make([]Node, 0, len(d.Nodes)),
		Edges: make([]Edge, 0, len(d.Edges)),
	}

	for _, n := range d.Nodes {
		node, err := toIRNode(n, idMap)
		if err != nil {
			return nil, err
		}
		ir.Nodes = append(ir.Nodes, node)
	}

	for _, e := range d.Edges {
		src, ok := idMap[e.SourceNodeID]
		if !ok {
			return nil, fmt.Errorf("toIR: edge source %q not found", e.SourceNodeID)
		}
		tgt, ok := idMap[e.TargetNodeID]
		if !ok {
			return nil, fmt.Errorf("toIR: edge target %q not found", e.TargetNodeID)
		}
		ir.Edges = append(ir.Edges, Edge{
			Source:     src,
			Target:     tgt,
			SourcePort: e.SourceHandle,
		})
	}

	return ir, nil
}

func toIRNode(n DSLNode, idMap map[string]string) (Node, error) {
	kind := nodeTypeOf(n)
	node := Node{
		ID:    idMap[n.ID],
		Kind:  kind,
		Title: n.Data.NodeMeta.AliasName,
	}

	// nodeParam 还原 app_id / dataset_id / workflow_id 与用户 params。
	params := map[string]any{}
	for k, v := range n.Data.NodeParam {
		switch {
		case kind == KindApplication && k == "appId":
			node.AppID = toStr(v)
		case kind == KindDataset && k == "datasetId":
			node.DatasetID = toStr(v)
		case kind == KindWorkflow && k == "workflowId":
			node.WorkflowID = toStr(v)
		case k == "branches":
			node.Branches = paramToBranches(v, idMap)
		case k == "batch":
			node.Batch = paramToBatch(v, idMap)
		default:
			params[k] = v
		}
	}
	if len(params) > 0 {
		node.Params = params
	}

	// 输入项：内联 value 提取成扁平 bindings + 端口声明。
	for _, in := range n.Data.Inputs {
		if in.Type() != "" {
			node.Inputs = append(node.Inputs, Port{Name: in.Name, Type: in.Type()})
		}
		b, ok := valueToBinding(in.Name, in.Schema.Value, idMap)
		if ok {
			node.Bindings = append(node.Bindings, b)
		}
	}

	// 输出项：还原端口声明。
	for _, out := range n.Data.Outputs {
		node.Outputs = append(node.Outputs, Port{
			Name:     out.Name,
			Type:     schemaType(out.Schema),
			Required: out.Required,
		})
	}

	return node, nil
}

// valueToBinding 把内联的 DSL Value 还原成扁平 Binding。
func valueToBinding(port string, v Value, idMap map[string]string) (Binding, bool) {
	switch v.Type {
	case DSLValueRef:
		ref, ok := asRefContent(v.Content)
		if !ok {
			return Binding{}, false
		}
		srcReadable, ok := idMap[ref.NodeID]
		if !ok {
			srcReadable = ref.NodeID // 容错：找不到就保留原值
		}
		return Binding{
			Port:       port,
			Mode:       BindModeRef,
			SourceNode: srcReadable,
			SourcePort: ref.Name,
		}, true
	case DSLValueLiteral:
		if v.Content == nil {
			return Binding{}, false
		}
		return Binding{Port: port, Mode: BindModeLiteral, Value: v.Content}, true
	default:
		return Binding{}, false
	}
}

// nodeTypeOf 优先用 nodeMeta.nodeType，回退到从 id 的 "type::uuid" 前缀取。
func nodeTypeOf(n DSLNode) string {
	if n.Data.NodeMeta.NodeType != "" {
		return n.Data.NodeMeta.NodeType
	}
	if i := strings.Index(n.ID, "::"); i > 0 {
		return n.ID[:i]
	}
	return n.ID
}

// Type 返回输入项的声明类型。
func (in InputItem) Type() string { return in.Schema.Type }

func schemaType(m map[string]any) string {
	if m == nil {
		return ""
	}
	if t, ok := m["type"].(string); ok {
		return t
	}
	return ""
}

// asRefContent 兼容 RefContent 结构体与反序列化后的 map。
func asRefContent(v any) (RefContent, bool) {
	switch c := v.(type) {
	case RefContent:
		return c, true
	case map[string]any:
		return RefContent{NodeID: toStr(c["nodeId"]), Name: toStr(c["name"])}, true
	default:
		return RefContent{}, false
	}
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// paramToBranches 把 nodeParam["branches"] 还原成 IR []Branch。
// 兼容内存态（[]any / int index）与 JSON 反序列化态（[]any / float64 index）。
// left_node 经 idMap 从 DSL ID 翻回可读 ID，实现 render↔ToIR 无损往返。
func paramToBranches(v any, idMap map[string]string) []Branch {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	branches := make([]Branch, 0, len(raw))
	for _, item := range raw {
		bm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		br := Branch{Index: toInt(bm["index"])}
		if conds, ok := bm["conditions"].([]any); ok {
			for _, c := range conds {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				br.Conditions = append(br.Conditions, Condition{
					LeftNode:   mapNodeID(toStr(cm["left_node"]), idMap),
					LeftPort:   toStr(cm["left_port"]),
					Comparator: toStr(cm["comparator"]),
					Right:      cm["right"],
					RightMode:  toStr(cm["right_mode"]),
				})
			}
		}
		branches = append(branches, br)
	}
	return branches
}

// paramToBatch 把 nodeParam["batch"] 还原成 IR *Batch。
func paramToBatch(v any, idMap map[string]string) *Batch {
	bm, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	b := &Batch{
		SourceNode: mapNodeID(toStr(bm["source_node"]), idMap),
		SourcePort: toStr(bm["source_port"]),
		ItemName:   toStr(bm["item_name"]),
		Size:       toInt(bm["size"]),
	}
	if e, ok := bm["enable"].(bool); ok {
		b.Enable = e
	}
	return b
}

// toInt 把 JSON 数字（float64）或内存态整型统一转 int。
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}
