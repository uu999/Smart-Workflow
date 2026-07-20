package dsl

// DSL（Domain Specific Language）是执行态工作流表示，引擎与存储只认它。
// 字段命名对齐 PaiFlow core/workflow/engine/entities/workflow_dsl.py，便于互导。
// 结构特征：嵌套 / 字段完整 / type::uuid ID / 绑定内联进 inputs。
type DSL struct {
	Nodes []DSLNode `json:"nodes"`
	Edges []DSLEdge `json:"edges"`
}

// DSLNode 执行态节点。ID 形如 application::a1b2c3。
type DSLNode struct {
	ID   string   `json:"id"`
	Data NodeData `json:"data"`
}

// NodeData 是节点的完整配置。
type NodeData struct {
	NodeMeta    NodeMeta       `json:"nodeMeta"`
	NodeParam   map[string]any `json:"nodeParam"`
	Inputs      []InputItem    `json:"inputs"`
	Outputs     []OutputItem   `json:"outputs"`
	RetryConfig RetryConfig    `json:"retryConfig"`
}

// NodeMeta 节点元信息。
type NodeMeta struct {
	NodeType  string `json:"nodeType"`
	AliasName string `json:"aliasName"`
}

// InputItem 输入项，绑定内联在 Schema.Value 中。
type InputItem struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Schema InputSchema `json:"schema"`
}

// InputSchema 输入 schema，含类型与取值来源。
type InputSchema struct {
	Type  string `json:"type"`
	Value Value  `json:"value"`
}

// Value 取值：type=ref 时 Content 为 RefContent；type=literal 时为标量。
type Value struct {
	Type    string `json:"type"`
	Content any    `json:"content"`
}

// RefContent 是 ref 绑定指向的上游节点输出。
type RefContent struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
}

// OutputItem 输出项。
type OutputItem struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Schema   map[string]any `json:"schema"`
	Required bool           `json:"required"`
}

// DSLEdge 执行态边。SourceHandle: ""=默认 / fail / 分支序号。
type DSLEdge struct {
	SourceNodeID string `json:"sourceNodeId"`
	TargetNodeID string `json:"targetNodeId"`
	SourceHandle string `json:"sourceHandle"`
}

// RetryConfig 节点级容错，对齐 PaiFlow retry_config.py。
type RetryConfig struct {
	Timeout       float64        `json:"timeout"`
	ShouldRetry   bool           `json:"shouldRetry"`
	MaxRetries    int            `json:"maxRetries"`
	ErrorStrategy int            `json:"errorStrategy"` // ErrStrategy*
	CustomOutput  map[string]any `json:"customOutput"`
}

// 值类型（ref / literal）。
const (
	DSLValueRef     = "ref"
	DSLValueLiteral = "literal"
)

// 错误处理策略，对齐 PaiFlow ErrorHandler。
const (
	ErrStrategyInterrupted  = 0 // 中断整个工作流
	ErrStrategyFailBranch   = 1 // 走失败分支
	ErrStrategyCustomReturn = 2 // 返回自定义兜底输出
)

// DefaultRetryConfig 返回节点默认容错配置：60s 超时、不重试、失败即中断。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Timeout:       60,
		ShouldRetry:   false,
		MaxRetries:    0,
		ErrorStrategy: ErrStrategyInterrupted,
		CustomOutput:  map[string]any{},
	}
}
