package dsl

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Renderer 把 IR 编译成 DSL（设计文档 §10.5）。
// IDGen 可注入以便测试产出确定性 ID；为空时用随机 hex。
type Renderer struct {
	IDGen func(kind string) string
}

// NewRenderer 返回使用随机 ID 的渲染器。
func NewRenderer() *Renderer {
	return &Renderer{IDGen: randomID}
}

func randomID(kind string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return kind + "::" + hex.EncodeToString(b)
}

// Render 执行 IR→DSL 六步编译。
func (r *Renderer) Render(ir *IR) (*DSL, error) {
	gen := r.IDGen
	if gen == nil {
		gen = randomID
	}

	// 步骤1：为每个 IR 节点生成真实 ID，建立 idMap[ir.id]=dsl.id。
	idMap := make(map[string]string, len(ir.Nodes))
	for _, n := range ir.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("render: node with empty id")
		}
		if _, dup := idMap[n.ID]; dup {
			return nil, fmt.Errorf("render: duplicate node id %q", n.ID)
		}
		idMap[n.ID] = gen(n.Kind)
	}

	out := &DSL{
		Nodes: make([]DSLNode, 0, len(ir.Nodes)),
		Edges: make([]DSLEdge, 0, len(ir.Edges)),
	}

	// 步骤2+3+5：展开节点结构、内联绑定、附加参数默认值。
	for _, n := range ir.Nodes {
		dn, err := r.renderNode(&n, idMap)
		if err != nil {
			return nil, err
		}
		out.Nodes = append(out.Nodes, dn)
	}

	// 步骤4：转换边。
	for _, e := range ir.Edges {
		src, ok := idMap[e.Source]
		if !ok {
			return nil, fmt.Errorf("render: edge source %q not found", e.Source)
		}
		tgt, ok := idMap[e.Target]
		if !ok {
			return nil, fmt.Errorf("render: edge target %q not found", e.Target)
		}
		out.Edges = append(out.Edges, DSLEdge{
			SourceNodeID: src,
			TargetNodeID: tgt,
			SourceHandle: e.SourcePort,
		})
	}

	return out, nil
}

// renderNode 展开单个节点：建骨架 → 内联绑定 → 补 nodeParam/retryConfig。
func (r *Renderer) renderNode(n *Node, idMap map[string]string) (DSLNode, error) {
	// 输入项：以 IR 的端口声明为基础，绑定内联进 Schema.Value。
	bindByPort := make(map[string]Binding, len(n.Bindings))
	for _, b := range n.Bindings {
		bindByPort[b.Port] = b
	}

	inputs := make([]InputItem, 0, len(n.Inputs))
	for _, p := range n.Inputs {
		item := InputItem{
			ID:     "in-" + p.Name,
			Name:   p.Name,
			Schema: InputSchema{Type: p.Type},
		}
		if b, ok := bindByPort[p.Name]; ok {
			val, err := r.renderValue(b, idMap)
			if err != nil {
				return DSLNode{}, fmt.Errorf("node %s port %s: %w", n.ID, p.Name, err)
			}
			item.Schema.Value = val
		}
		inputs = append(inputs, item)
	}

	// 若某个 binding 的 port 不在 inputs 声明里（半成品编辑态允许），也补一个输入项。
	for _, b := range n.Bindings {
		if !hasInput(inputs, b.Port) {
			val, err := r.renderValue(b, idMap)
			if err != nil {
				return DSLNode{}, fmt.Errorf("node %s port %s: %w", n.ID, b.Port, err)
			}
			inputs = append(inputs, InputItem{
				ID:     "in-" + b.Port,
				Name:   b.Port,
				Schema: InputSchema{Value: val},
			})
		}
	}

	outputs := make([]OutputItem, 0, len(n.Outputs))
	for _, p := range n.Outputs {
		outputs = append(outputs, OutputItem{
			ID:       "out-" + p.Name,
			Name:     p.Name,
			Required: p.Required,
			Schema:   map[string]any{"type": p.Type},
		})
	}

	// nodeParam：附加 appId / 用户 params。
	nodeParam := map[string]any{}
	for k, v := range n.Params {
		nodeParam[k] = v
	}
	switch n.Kind {
	case KindApplication:
		if n.AppID != "" {
			nodeParam["appId"] = n.AppID
		}
	case KindDataset:
		if n.DatasetID != "" {
			nodeParam["datasetId"] = n.DatasetID
		}
	case KindWorkflow:
		if n.WorkflowID != "" {
			nodeParam["workflowId"] = n.WorkflowID
		}
	}

	// condition 分支：序列化进 nodeParam，格式对齐 ConditionExecutor 读取
	// （[]any of map[string]any，index/conditions），使 IR 构造的 condition
	// 节点渲染出的 DSL 能被引擎执行——此前 branches 被静默丢弃，导致
	// condition 节点执行期报 "has no branches"。
	if len(n.Branches) > 0 {
		nodeParam["branches"] = branchesToParam(n.Branches, idMap)
	}
	// batch：预留字段，随节点保留（MVP 无执行器，仅防 render/clone 静默丢弃）。
	if n.Batch != nil {
		nodeParam["batch"] = batchToParam(n.Batch, idMap)
	}

	alias := n.Title
	if alias == "" {
		alias = n.Kind
	}

	return DSLNode{
		ID: idMap[n.ID],
		Data: NodeData{
			NodeMeta:    NodeMeta{NodeType: n.Kind, AliasName: alias},
			NodeParam:   nodeParam,
			Inputs:      inputs,
			Outputs:     outputs,
			RetryConfig: DefaultRetryConfig(),
		},
	}, nil
}

// renderValue 把一个 binding 转成 DSL 的 Value（ref / literal）。
func (r *Renderer) renderValue(b Binding, idMap map[string]string) (Value, error) {
	switch b.resolvedMode() {
	case BindModeRef:
		srcID, ok := idMap[b.SourceNode]
		if !ok {
			return Value{}, fmt.Errorf("ref source node %q not found", b.SourceNode)
		}
		return Value{
			Type:    DSLValueRef,
			Content: RefContent{NodeID: srcID, Name: b.SourcePort},
		}, nil
	case BindModeLiteral:
		return Value{Type: DSLValueLiteral, Content: b.Value}, nil
	case BindModeClear:
		return Value{}, nil
	default:
		return Value{}, fmt.Errorf("unknown bind mode %q", b.Mode)
	}
}

func hasInput(inputs []InputItem, name string) bool {
	for _, it := range inputs {
		if it.Name == name {
			return true
		}
	}
	return false
}

// branchesToParam 把 IR condition 分支序列化成 ConditionExecutor 认识的形态：
// []any of map[string]any{index, conditions[]}，条件字段用 snake_case
// （left_node/left_port/comparator/right/right_mode），与 condition.go 读取一致。
// left_node 经 idMap 翻译成 DSL 真实 ID，保持 DSL 内引用自洽。
func branchesToParam(branches []Branch, idMap map[string]string) []any {
	out := make([]any, 0, len(branches))
	for _, br := range branches {
		conds := make([]any, 0, len(br.Conditions))
		for _, c := range br.Conditions {
			cm := map[string]any{
				"left_node":  mapNodeID(c.LeftNode, idMap),
				"left_port":  c.LeftPort,
				"comparator": c.Comparator,
				"right":      c.Right,
			}
			if c.RightMode != "" {
				cm["right_mode"] = c.RightMode
			}
			conds = append(conds, cm)
		}
		out = append(out, map[string]any{
			"index":      br.Index,
			"conditions": conds,
		})
	}
	return out
}

// batchToParam 把 IR Batch 序列化进 nodeParam（MVP 无执行器，仅防丢失）。
func batchToParam(b *Batch, idMap map[string]string) map[string]any {
	m := map[string]any{
		"enable":      b.Enable,
		"source_node": mapNodeID(b.SourceNode, idMap),
		"source_port": b.SourcePort,
	}
	if b.ItemName != "" {
		m["item_name"] = b.ItemName
	}
	if b.Size != 0 {
		m["size"] = b.Size
	}
	return m
}

// mapNodeID 用 idMap 翻译节点 ID；不在表中（半成品/悬空引用）时保留原值，
// 保证 render↔ToIR 对悬空引用是恒等往返。
func mapNodeID(id string, idMap map[string]string) string {
	if id == "" {
		return ""
	}
	if mapped, ok := idMap[id]; ok {
		return mapped
	}
	return id
}
