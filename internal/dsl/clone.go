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
