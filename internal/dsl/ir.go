package dsl

// IR（Intermediate Representation）是编辑态工作流表示，供 Agent / CLI 操作。
// 设计原则见 Smart-Workflow_Agent-CLI设计.md §10：
// 扁平 / 关系与结构分离 / 字段最小 / 容忍半成品 / 可读 ID / 视图与执行分离。
type IR struct {
	Meta   Meta               `json:"meta"`
	Nodes  []Node             `json:"nodes"`
	Edges  []Edge             `json:"edges"`
	Layout map[string]NodePos `json:"layout,omitempty"` // 改④：视图与执行分离，node_id -> 坐标
}

// Meta 是工作流元信息。
type Meta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Source      string `json:"source,omitempty"` // clone-ref 来源工作流ID
}

// Node 是编辑态节点。可读 ID（如 start_0 / app_1），字段最小。
type Node struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title,omitempty"`
	AppID      string         `json:"app_id,omitempty"`
	DatasetID  string         `json:"dataset_id,omitempty"`
	WorkflowID string         `json:"workflow_id,omitempty"`
	Inputs     []Port         `json:"inputs,omitempty"`  // 改③：端口声明，来自 app-schema
	Outputs    []Port         `json:"outputs,omitempty"` // 改③：静态连线校验地基
	Bindings   []Binding      `json:"bindings,omitempty"`
	Branches   []Branch       `json:"branches,omitempty"` // condition 专用
	Batch      *Batch         `json:"batch,omitempty"`
	Params     map[string]any `json:"params,omitempty"` // 节点私有参数(code/prompt/template)
}

// Port 是端口声明（编辑态）。
type Port struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // ValueType*
	Required bool   `json:"required,omitempty"`
}

// Binding 是扁平的输入绑定，关系独立于节点结构。
type Binding struct {
	Port       string `json:"port"`
	Mode       string `json:"mode,omitempty"` // BindMode*，缺省推断
	SourceNode string `json:"source_node,omitempty"`
	SourcePort string `json:"source_port,omitempty"`
	Scope      string `json:"scope,omitempty"` // 改⑤预留：""=当前 / parent / root
	Value      any    `json:"value,omitempty"`
	ValueType  string `json:"value_type,omitempty"`
}

// Edge 是控制流边。source_port 用于 condition 多路输出（分支序号字符串）。
type Edge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	SourcePort string `json:"source_port,omitempty"`
}

// Branch 是 condition 节点的一个分支。
type Branch struct {
	Index      int         `json:"index"`
	Conditions []Condition `json:"conditions"`
}

// Condition 是分支内的一个判定表达式。
type Condition struct {
	LeftNode   string `json:"left_node"`
	LeftPort   string `json:"left_port"`
	Comparator string `json:"comparator"` // eq/ne/gt/gte/lt/lte/contains
	Right      any    `json:"right"`
	RightMode  string `json:"right_mode,omitempty"` // literal/ref
}

// Batch 表示节点对数组逐项执行。
type Batch struct {
	Enable     bool   `json:"enable"`
	SourceNode string `json:"source_node"`
	SourcePort string `json:"source_port"`
	ItemName   string `json:"item_name,omitempty"`
	Size       int    `json:"size,omitempty"`
}

// NodePos 是画布坐标（视图数据）。
type NodePos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// 节点类型 Kind。
const (
	KindStart       = "start"
	KindEnd         = "end"
	KindApplication = "application"
	KindDataset     = "dataset"
	KindWorkflow    = "workflow"
	KindCondition   = "condition"
	KindCode        = "code"
)

// 绑定模式 BindMode。
const (
	BindModeRef     = "ref"
	BindModeLiteral = "literal"
	BindModeClear   = "clear"
)

// 值类型 ValueType（对齐 DSL InputSchema.Type）。
const (
	ValueTypeString  = "string"
	ValueTypeInteger = "integer"
	ValueTypeNumber  = "number"
	ValueTypeBoolean = "boolean"
	ValueTypeArray   = "array"
	ValueTypeObject  = "object"
)

// FindNode 按 ID 返回节点指针，不存在返回 nil。
func (ir *IR) FindNode(id string) *Node {
	for i := range ir.Nodes {
		if ir.Nodes[i].ID == id {
			return &ir.Nodes[i]
		}
	}
	return nil
}

// FindOutput 按端口名返回节点的输出声明，不存在返回 nil。
func (n *Node) FindOutput(name string) *Port {
	for i := range n.Outputs {
		if n.Outputs[i].Name == name {
			return &n.Outputs[i]
		}
	}
	return nil
}

// resolvedMode 返回 binding 的有效模式：缺省时按 §10.5 规则推断
// （有 source_node 判 ref，否则 literal）。
func (b *Binding) resolvedMode() string {
	if b.Mode != "" {
		return b.Mode
	}
	if b.SourceNode != "" {
		return BindModeRef
	}
	return BindModeLiteral
}
