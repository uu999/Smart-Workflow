# M1 IR / DSL 设计与代码解读

本文用于精读 Smart-Workflow 的 M1 代码，覆盖：

- `internal/dsl/ir.go`：Agent / CLI 编辑态 IR
- `internal/dsl/dsl.go`：引擎 / 存储执行态 DSL
- `internal/dsl/render.go`：IR -> DSL 编译器
- `internal/dsl/clone.go`：DSL -> IR 反编译器
- `internal/dsl/dsl_test.go`：M1 的测试规格

先给结论：

```text
IR 是给 Agent/CLI 编辑的“源代码”。
DSL 是给引擎执行、给 MySQL JSON 列存储的“编译产物”。
render 就是把 IR 转换成 DSL，但它只做结构转换，不负责完整校验、不执行工作流、不落库。
```

---

## 1. 先回答几个最容易混淆的问题

### 1.1 `mode` 和 `source_node` 的区别是什么？

它们不是同一类信息。

```text
mode        表示“这个输入值怎么来”
source_node 表示“如果这个输入值来自上游节点，那上游节点是谁”
```

也就是说：

- `mode` 是取值方式。
- `source_node` 是 `mode=ref` 时的来源坐标之一。
- `source_port` 是 `mode=ref` 时的另一个来源坐标。

IR 里的绑定结构是：

```go
type Binding struct {
    Port       string `json:"port"`
    Mode       string `json:"mode,omitempty"`
    SourceNode string `json:"source_node,omitempty"`
    SourcePort string `json:"source_port,omitempty"`
    Scope      string `json:"scope,omitempty"`
    Value      any    `json:"value,omitempty"`
    ValueType  string `json:"value_type,omitempty"`
}
```

例如：

```json
{
  "port": "text",
  "mode": "ref",
  "source_node": "start_0",
  "source_port": "query"
}
```

含义是：

```text
当前节点的 text 输入，不是写死的常量，而是引用 start_0 节点的 query 输出。
```

再看 literal：

```json
{
  "port": "prompt",
  "mode": "literal",
  "value": "请总结下面这段文本"
}
```

含义是：

```text
当前节点的 prompt 输入，直接使用这个固定字符串。
```

所以可以这样记：

```text
mode = ref      -> 看 source_node + source_port
mode = literal  -> 看 value
mode = clear    -> 清空这个输入
```

### 1.2 `ref` 和 `literal` 分别代表什么？

`ref` 是引用上游节点输出。

```text
ref = runtime reference
```

它不在构建工作流时就有值，而是在工作流运行时，从变量池里取值。

例如：

```json
{
  "port": "text",
  "mode": "ref",
  "source_node": "start_0",
  "source_port": "query"
}
```

运行时大概等价于：

```go
text = variablePool.Get("start_0", "query")
```

`literal` 是常量值。

```text
literal = 写死在工作流定义里的常量
```

例如：

```json
{
  "port": "temperature",
  "mode": "literal",
  "value": 0.7
}
```

运行时不需要查上游节点，直接使用 `0.7`。

### 1.3 如果 `mode` 不写，系统怎么判断？

看 `ir.go` 里的 `resolvedMode()`：

```go
func (b *Binding) resolvedMode() string {
    if b.Mode != "" {
        return b.Mode
    }
    if b.SourceNode != "" {
        return BindModeRef
    }
    return BindModeLiteral
}
```

规则是：

```text
显式写了 mode             -> 使用显式 mode
没写 mode，但有 source_node -> 推断为 ref
没写 mode，也没有 source_node -> 推断为 literal
```

所以这两个写法等价：

```json
{
  "port": "text",
  "mode": "ref",
  "source_node": "start_0",
  "source_port": "query"
}
```

```json
{
  "port": "text",
  "source_node": "start_0",
  "source_port": "query"
}
```

后者更适合 Agent 小步编辑，因为可以少填字段。

### 1.4 render 就是把 IR 转换成 DSL 吗？

是。更准确地说：

```text
render 是一个编译过程：IR -> DSL
```

但要注意它“不做什么”：

- 不负责完整业务校验。完整校验由 M3 的 validator 做。
- 不执行工作流。执行由 M4 的 engine/scheduler 做。
- 不写 MySQL。存储由 M2 的 service/storage 做。
- 不自动猜端口。端口绑定应由 Agent/CLI 在 IR 的 bindings 里明确表达。

render 主要做结构转换：

```text
可读 ID -> 执行态 ID
扁平 binding -> DSL 内联 schema.value
IR edge -> DSL edge
IR params/app_id -> DSL nodeParam
IR ports -> DSL inputs/outputs
补默认 retryConfig
```

---

## 2. M1 总体结构

M1 代码都在 `internal/dsl/`：

```text
internal/dsl/
├── ir.go        # 编辑态 IR
├── dsl.go       # 执行态 DSL
├── render.go    # IR -> DSL
├── clone.go     # DSL -> IR
├── deps.go      # 执行依赖构建：edges ∪ ref bindings
└── dsl_test.go  # M1 测试
```

主线是：

```text
Agent/CLI 生成或修改 IR
        |
        | render
        v
引擎可以执行的 DSL
        |
        | 存入 workflow.draft_dsl / workflow_version.dsl
        v
MySQL JSON 列
```

反向链路是：

```text
已有 DSL
  |
  | ToIR
  v
可编辑 IR
```

这用于后续 `clone-ref`：优先复制相似工作流，再让 Agent 小步修改。

---

## 3. 为什么要分 IR 和 DSL 两层？

如果只有 DSL，一开始就让 Agent 拼这种结构：

```json
{
  "id": "application::abc123",
  "data": {
    "nodeMeta": {"nodeType": "application"},
    "nodeParam": {"appId": "12345"},
    "inputs": [
      {
        "name": "text",
        "schema": {
          "type": "string",
          "value": {
            "type": "ref",
            "content": {
              "nodeId": "start::xyz789",
              "name": "query"
            }
          }
        }
      }
    ]
  }
}
```

它很容易失败：

- ID 是 `type::uuid`，Agent 不容易稳定引用。
- 输入绑定嵌得很深，小步编辑成本高。
- 改一条绑定，需要改节点内部多层字段。
- 半成品状态不友好，节点还没补 schema 时不容易表达。
- layout、执行配置、节点参数混在一起会增加误改概率。

所以 Smart-Workflow 设计了 IR：

```json
{
  "id": "app_1",
  "kind": "application",
  "app_id": "12345",
  "inputs": [{"name": "text", "type": "string", "required": true}],
  "bindings": [
    {
      "port": "text",
      "source_node": "start_0",
      "source_port": "query"
    }
  ]
}
```

这对 Agent 更友好：

- ID 可读：`start_0`、`app_1`、`end_0`
- binding 扁平：一眼能看出哪个端口接哪里
- edges 独立：改连线不用改节点内部
- layout 分离：坐标不污染执行语义
- 允许半成品：先加节点，再补端口，再绑定

一句话：

```text
IR 追求“好编辑”，DSL 追求“可执行”。
```

---

## 4. IR 字段逐个解释

源码：`internal/dsl/ir.go`

### 4.1 `IR`

```go
type IR struct {
    Meta   Meta               `json:"meta"`
    Nodes  []Node             `json:"nodes"`
    Edges  []Edge             `json:"edges"`
    Layout map[string]NodePos `json:"layout,omitempty"`
}
```

| 字段 | 类型 | 用途 |
|---|---|---|
| `Meta` | `Meta` | 工作流元信息，给 CLI/session/展示使用 |
| `Nodes` | `[]Node` | 节点列表，编辑态节点 |
| `Edges` | `[]Edge` | 控制流边，表示节点执行顺序或 condition 分支流向 |
| `Layout` | `map[string]NodePos` | 画布坐标，纯视图数据，不参与执行 |

注意：`Layout` 单独放，而不是塞进 `Node`，是为了避免 UI 坐标污染执行语义。

### 4.2 `Meta`

```go
type Meta struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    ProjectID   string `json:"project_id,omitempty"`
    Source      string `json:"source,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Name` | 工作流名称 |
| `Description` | 工作流描述 |
| `ProjectID` | 项目 ID，后续资源隔离、能力发现会用 |
| `Source` | 来源工作流 ID，给 `clone-ref` 标记“这个 IR 是从哪个已有工作流克隆来的” |

### 4.3 `Node`

```go
type Node struct {
    ID         string         `json:"id"`
    Kind       string         `json:"kind"`
    Title      string         `json:"title,omitempty"`
    AppID      string         `json:"app_id,omitempty"`
    DatasetID  string         `json:"dataset_id,omitempty"`
    WorkflowID string         `json:"workflow_id,omitempty"`
    Inputs     []Port         `json:"inputs,omitempty"`
    Outputs    []Port         `json:"outputs,omitempty"`
    Bindings   []Binding      `json:"bindings,omitempty"`
    Branches   []Branch       `json:"branches,omitempty"`
    Batch      *Batch         `json:"batch,omitempty"`
    Params     map[string]any `json:"params,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `ID` | 编辑态可读 ID，如 `start_0`、`app_1`。Agent/CLI 用它做引用 |
| `Kind` | 节点类型，如 `start/end/application/condition/code` |
| `Title` | 展示名，render 后进入 DSL 的 `nodeMeta.aliasName` |
| `AppID` | application 节点引用的应用 ID，render 后进入 `nodeParam.appId` |
| `DatasetID` | dataset 节点引用的数据集 ID，render 后进入 `nodeParam.datasetId` |
| `WorkflowID` | workflow 节点引用的子工作流 ID，render 后进入 `nodeParam.workflowId` |
| `Inputs` | 输入端口声明，来自 app-schema 或手写声明 |
| `Outputs` | 输出端口声明，给静态校验和 scope 绑定候选使用 |
| `Bindings` | 当前节点输入端口如何取值 |
| `Branches` | condition 节点的分支表达式 |
| `Batch` | 批处理配置，表示对数组逐项执行 |
| `Params` | 节点私有参数，如 http 的 url/method，end 的 template，code 的源码 |

关键点：

```text
Inputs/Outputs 是“端口契约”
Bindings 是“输入端口如何取值”
Edges 是“控制流怎么走”
Params 是“节点自己的配置”
```

这几个概念不要混。

### 4.4 `Port`

```go
type Port struct {
    Name     string `json:"name"`
    Type     string `json:"type"`
    Required bool   `json:"required,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Name` | 端口名，如 `query`、`text`、`label` |
| `Type` | 端口值类型，如 `string/number/object/array` |
| `Required` | 是否必填，validator 会检查必填输入是否绑定 |

端口声明用于：

- `validate` 检查引用端口是否存在。
- `validate` 检查类型是否匹配。
- `scope` 以后列出“这个输入可以接哪些上游输出”。
- render 时生成 DSL 的 `inputs/outputs`。

### 4.5 `Binding`

```go
type Binding struct {
    Port       string `json:"port"`
    Mode       string `json:"mode,omitempty"`
    SourceNode string `json:"source_node,omitempty"`
    SourcePort string `json:"source_port,omitempty"`
    Scope      string `json:"scope,omitempty"`
    Value      any    `json:"value,omitempty"`
    ValueType  string `json:"value_type,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Port` | 当前节点的哪个输入端口要绑定 |
| `Mode` | 取值模式：`ref/literal/clear` |
| `SourceNode` | `ref` 模式下，上游节点 ID |
| `SourcePort` | `ref` 模式下，上游输出端口名，可带路径，如 `json.message` |
| `Scope` | 作用域预留，后续子流 / batch / 嵌套工作流会用 |
| `Value` | `literal` 模式下的常量值 |
| `ValueType` | literal 值类型提示，给校验和 Agent 辅助使用 |

常见写法：

```json
{
  "port": "text",
  "mode": "ref",
  "source_node": "start_0",
  "source_port": "query"
}
```

```json
{
  "port": "timeout",
  "mode": "literal",
  "value": 30,
  "value_type": "number"
}
```

```json
{
  "port": "text",
  "mode": "clear"
}
```

需要注意：

```text
source_node/source_port 只有在 ref 模式下才有意义。
value 只有在 literal 模式下才有意义。
mode 可以省略，但 source_node 不应乱填。
```

### 4.6 `Edge`

```go
type Edge struct {
    Source     string `json:"source"`
    Target     string `json:"target"`
    SourcePort string `json:"source_port,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Source` | 控制流起点节点 ID |
| `Target` | 控制流终点节点 ID |
| `SourcePort` | condition 分支句柄，如 `"0"`、`"1"`；普通边为空 |

普通边：

```json
{"source": "start_0", "target": "app_1"}
```

condition 分支边：

```json
{"source": "condition_0", "target": "end_pass", "source_port": "0"}
```

注意：`Edge` 表示控制流，但执行依赖不只来自 `Edge`。后面 `deps.go` 会把 `ref binding` 也补成依赖：

```text
执行依赖 = edges ∪ ref bindings
```

### 4.7 `Branch` 和 `Condition`

```go
type Branch struct {
    Index      int         `json:"index"`
    Conditions []Condition `json:"conditions"`
}
```

```go
type Condition struct {
    LeftNode   string `json:"left_node"`
    LeftPort   string `json:"left_port"`
    Comparator string `json:"comparator"`
    Right      any    `json:"right"`
    RightMode  string `json:"right_mode,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Branch.Index` | 分支编号，对应 edge 的 `source_port` |
| `Branch.Conditions` | 当前分支的条件列表，多个条件通常按 AND 处理 |
| `LeftNode` | 条件左侧引用哪个节点 |
| `LeftPort` | 条件左侧引用哪个输出端口 |
| `Comparator` | 比较器，如 `eq/ne/gt/gte/lt/lte/contains` |
| `Right` | 右侧值 |
| `RightMode` | 右侧是 literal 还是 ref，当前主要预留 |

示例：

```json
{
  "index": 0,
  "conditions": [
    {
      "left_node": "start_0",
      "left_port": "score",
      "comparator": "gte",
      "right": 0.8
    }
  ]
}
```

含义：

```text
如果 start_0.score >= 0.8，则命中分支 0。
```

### 4.8 `Batch`

```go
type Batch struct {
    Enable     bool   `json:"enable"`
    SourceNode string `json:"source_node"`
    SourcePort string `json:"source_port"`
    ItemName   string `json:"item_name,omitempty"`
    Size       int    `json:"size,omitempty"`
}
```

| 字段 | 用途 |
|---|---|
| `Enable` | 是否启用批处理 |
| `SourceNode` | 批处理数组来自哪个节点 |
| `SourcePort` | 批处理数组来自哪个输出端口 |
| `ItemName` | 每个元素在子执行上下文里的名字 |
| `Size` | 批大小 |

M1 只是设计结构，执行逻辑还没展开。

### 4.9 `NodePos`

```go
type NodePos struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}
```

只用于画布展示，不参与 render 的执行语义转换。

---

## 5. DSL 字段逐个解释

源码：`internal/dsl/dsl.go`

### 5.1 `DSL`

```go
type DSL struct {
    Nodes []DSLNode `json:"nodes"`
    Edges []DSLEdge `json:"edges"`
}
```

| 字段 | 用途 |
|---|---|
| `Nodes` | 执行态节点列表 |
| `Edges` | 执行态控制边 |

DSL 是最终入库和执行的结构。

### 5.2 `DSLNode`

```go
type DSLNode struct {
    ID   string   `json:"id"`
    Data NodeData `json:"data"`
}
```

| 字段 | 用途 |
|---|---|
| `ID` | 执行态节点 ID，如 `application::a1b2c3` |
| `Data` | 节点完整配置 |

和 IR 的 `Node.ID` 不同：

```text
IR ID  = app_1                 给 Agent/CLI 看
DSL ID = application::a1b2c3    给引擎/存储看
```

### 5.3 `NodeData`

```go
type NodeData struct {
    NodeMeta    NodeMeta       `json:"nodeMeta"`
    NodeParam   map[string]any `json:"nodeParam"`
    Inputs      []InputItem    `json:"inputs"`
    Outputs     []OutputItem   `json:"outputs"`
    RetryConfig RetryConfig    `json:"retryConfig"`
}
```

| 字段 | 用途 |
|---|---|
| `NodeMeta` | 节点类型和展示名 |
| `NodeParam` | 节点私有参数，如 appId、http url、end template |
| `Inputs` | 输入端口，绑定已经内联进去 |
| `Outputs` | 输出端口声明 |
| `RetryConfig` | 节点超时、重试、失败策略 |

### 5.4 `NodeMeta`

```go
type NodeMeta struct {
    NodeType  string `json:"nodeType"`
    AliasName string `json:"aliasName"`
}
```

| 字段 | 用途 |
|---|---|
| `NodeType` | 节点类型，如 `start/end/http/condition/application` |
| `AliasName` | 节点展示名 |

render 时：

```text
IR.Kind  -> DSL.NodeMeta.NodeType
IR.Title -> DSL.NodeMeta.AliasName
```

如果 `IR.Title` 为空，`AliasName` 默认用 `Kind`。

### 5.5 `InputItem` / `InputSchema` / `Value`

```go
type InputItem struct {
    ID     string      `json:"id"`
    Name   string      `json:"name"`
    Schema InputSchema `json:"schema"`
}
```

```go
type InputSchema struct {
    Type  string `json:"type"`
    Value Value  `json:"value"`
}
```

```go
type Value struct {
    Type    string `json:"type"`
    Content any    `json:"content"`
}
```

| 字段 | 用途 |
|---|---|
| `InputItem.ID` | 输入项 ID，当前 render 生成 `in-端口名` |
| `InputItem.Name` | 输入端口名 |
| `InputSchema.Type` | 输入类型 |
| `Value.Type` | 取值模式：`ref/literal` |
| `Value.Content` | 具体内容。ref 时是 `RefContent`，literal 时是常量 |

DSL 把 binding 内联进 `schema.value`。

ref 示例：

```json
{
  "id": "in-text",
  "name": "text",
  "schema": {
    "type": "string",
    "value": {
      "type": "ref",
      "content": {
        "nodeId": "start::start0",
        "name": "query"
      }
    }
  }
}
```

literal 示例：

```json
{
  "id": "in-temperature",
  "name": "temperature",
  "schema": {
    "type": "number",
    "value": {
      "type": "literal",
      "content": 0.7
    }
  }
}
```

### 5.6 `RefContent`

```go
type RefContent struct {
    NodeID string `json:"nodeId"`
    Name   string `json:"name"`
}
```

| 字段 | 用途 |
|---|---|
| `NodeID` | 上游 DSL 节点 ID |
| `Name` | 上游输出端口名，可带路径 |

注意这里用的是 DSL ID，不是 IR ID。

```text
IR:  SourceNode = start_0
DSL: RefContent.NodeID = start::start0
```

### 5.7 `OutputItem`

```go
type OutputItem struct {
    ID       string         `json:"id"`
    Name     string         `json:"name"`
    Schema   map[string]any `json:"schema"`
    Required bool           `json:"required"`
}
```

| 字段 | 用途 |
|---|---|
| `ID` | 输出项 ID，当前 render 生成 `out-端口名` |
| `Name` | 输出端口名 |
| `Schema` | 输出 schema，目前至少包含 `type` |
| `Required` | 该输出是否必需 |

render 时：

```text
IR.Outputs[] -> DSL.Outputs[]
```

### 5.8 `DSLEdge`

```go
type DSLEdge struct {
    SourceNodeID string `json:"sourceNodeId"`
    TargetNodeID string `json:"targetNodeId"`
    SourceHandle string `json:"sourceHandle"`
}
```

| 字段 | 用途 |
|---|---|
| `SourceNodeID` | 起点 DSL 节点 ID |
| `TargetNodeID` | 终点 DSL 节点 ID |
| `SourceHandle` | condition 分支句柄；普通边为空 |

render 时：

```text
IR.Edge.Source     -> idMap 后的 SourceNodeID
IR.Edge.Target     -> idMap 后的 TargetNodeID
IR.Edge.SourcePort -> SourceHandle
```

### 5.9 `RetryConfig`

```go
type RetryConfig struct {
    Timeout       float64        `json:"timeout"`
    ShouldRetry   bool           `json:"shouldRetry"`
    MaxRetries    int            `json:"maxRetries"`
    ErrorStrategy int            `json:"errorStrategy"`
    CustomOutput  map[string]any `json:"customOutput"`
}
```

| 字段 | 用途 |
|---|---|
| `Timeout` | 节点执行超时时间，单位秒 |
| `ShouldRetry` | 是否重试 |
| `MaxRetries` | 最大重试次数 |
| `ErrorStrategy` | 失败策略 |
| `CustomOutput` | 自定义失败兜底输出 |

M1 render 会填默认值：

```go
Timeout:       60
ShouldRetry:   false
MaxRetries:    0
ErrorStrategy: ErrStrategyInterrupted
CustomOutput:  map[string]any{}
```

---

## 6. Renderer 逐步解读

源码：`internal/dsl/render.go`

### 6.1 Renderer 是什么？

```go
type Renderer struct {
    IDGen func(kind string) string
}
```

`Renderer` 是 IR -> DSL 的编译器。

`IDGen` 用于生成 DSL 节点 ID：

- 测试时注入确定性 ID，便于断言。
- 正常运行时使用随机 ID。

默认 ID 形如：

```text
kind::随机hex
```

例如：

```text
start::a1b2c3
application::f9e8d7
```

### 6.2 Render 的主流程

入口：

```go
func (r *Renderer) Render(ir *IR) (*DSL, error)
```

主流程：

```text
1. 准备 ID 生成器
2. 遍历 IR 节点，生成 DSL ID，建立 idMap
3. 渲染每个节点
4. 渲染每条边
5. 返回 DSL
```

最重要的是 `idMap`：

```go
idMap := make(map[string]string, len(ir.Nodes))
for _, n := range ir.Nodes {
    idMap[n.ID] = gen(n.Kind)
}
```

示例：

```text
idMap["start_0"] = "start::start0"
idMap["app_1"]   = "application::application0"
idMap["end_0"]   = "end::end0"
```

它解决了一个核心问题：

```text
IR 里所有引用用可读 ID。
DSL 里所有引用必须改成执行态 ID。
```

### 6.3 renderNode 做什么？

入口：

```go
func (r *Renderer) renderNode(n *Node, idMap map[string]string) (DSLNode, error)
```

它把一个 IR 节点变成一个 DSL 节点。

#### 第一步：把 bindings 按 port 建索引

```go
bindByPort := make(map[string]Binding, len(n.Bindings))
for _, b := range n.Bindings {
    bindByPort[b.Port] = b
}
```

这样后面处理输入端口时，可以快速知道这个端口有没有绑定。

#### 第二步：根据 Inputs 生成 DSL Inputs

```go
for _, p := range n.Inputs {
    item := InputItem{
        ID:     "in-" + p.Name,
        Name:   p.Name,
        Schema: InputSchema{Type: p.Type},
    }
    if b, ok := bindByPort[p.Name]; ok {
        val, err := r.renderValue(b, idMap)
        item.Schema.Value = val
    }
    inputs = append(inputs, item)
}
```

这一步完成：

```text
IR.Inputs[] + IR.Bindings[] -> DSL.Inputs[].Schema.Value
```

#### 第三步：补充“有 binding 但没声明 input”的端口

```go
for _, b := range n.Bindings {
    if !hasInput(inputs, b.Port) {
        val, err := r.renderValue(b, idMap)
        inputs = append(inputs, InputItem{
            ID:     "in-" + b.Port,
            Name:   b.Port,
            Schema: InputSchema{Value: val},
        })
    }
}
```

为什么需要这个？

因为 IR 是编辑态，允许半成品。Agent 可能先写了 binding，但还没补完整 input schema。

这体现了 IR 的设计目标：

```text
允许半成品，错误留给 validate 集中报。
```

#### 第四步：渲染 outputs

```go
for _, p := range n.Outputs {
    outputs = append(outputs, OutputItem{
        ID:       "out-" + p.Name,
        Name:     p.Name,
        Required: p.Required,
        Schema:   map[string]any{"type": p.Type},
    })
}
```

转换关系：

```text
IR.Port{Name, Type, Required}
  -> DSL.OutputItem{Name, Schema.type, Required}
```

#### 第五步：渲染 nodeParam

```go
nodeParam := map[string]any{}
for k, v := range n.Params {
    nodeParam[k] = v
}
```

先复制 `Params`。

然后按节点类型补特殊 ID：

```go
switch n.Kind {
case KindApplication:
    nodeParam["appId"] = n.AppID
case KindDataset:
    nodeParam["datasetId"] = n.DatasetID
case KindWorkflow:
    nodeParam["workflowId"] = n.WorkflowID
}
```

也就是：

```text
IR.AppID      -> DSL.NodeParam["appId"]
IR.DatasetID  -> DSL.NodeParam["datasetId"]
IR.WorkflowID -> DSL.NodeParam["workflowId"]
IR.Params     -> DSL.NodeParam 其它字段
```

#### 第六步：生成最终 DSLNode

```go
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
```

### 6.4 renderValue 做什么？

入口：

```go
func (r *Renderer) renderValue(b Binding, idMap map[string]string) (Value, error)
```

它把 IR 的扁平 binding 转成 DSL 的内联 value。

#### ref

IR：

```json
{
  "port": "text",
  "mode": "ref",
  "source_node": "start_0",
  "source_port": "query"
}
```

DSL：

```json
{
  "type": "ref",
  "content": {
    "nodeId": "start::start0",
    "name": "query"
  }
}
```

注意：

```text
source_node 从 IR ID 变成 DSL ID。
source_port 原样进入 RefContent.Name。
```

#### literal

IR：

```json
{
  "port": "temperature",
  "mode": "literal",
  "value": 0.7
}
```

DSL：

```json
{
  "type": "literal",
  "content": 0.7
}
```

#### clear

IR：

```json
{
  "port": "text",
  "mode": "clear"
}
```

DSL：

```json
{}
```

当前实现返回空 `Value{}`。

---

## 7. 一个完整例子：start -> app -> end

M1 测试里的 `sampleIR()` 是最好的学习入口。

### 7.1 IR 输入

简化后：

```json
{
  "meta": {
    "name": "情感评测",
    "project_id": "6970"
  },
  "nodes": [
    {
      "id": "start_0",
      "kind": "start",
      "outputs": [
        {"name": "query", "type": "string", "required": true}
      ]
    },
    {
      "id": "app_1",
      "kind": "application",
      "app_id": "12345",
      "title": "情感分类器",
      "inputs": [
        {"name": "text", "type": "string", "required": true}
      ],
      "outputs": [
        {"name": "label", "type": "string"},
        {"name": "confidence", "type": "number"}
      ],
      "bindings": [
        {
          "port": "text",
          "mode": "ref",
          "source_node": "start_0",
          "source_port": "query"
        }
      ]
    },
    {
      "id": "end_0",
      "kind": "end",
      "inputs": [
        {"name": "label", "type": "string"},
        {"name": "confidence", "type": "number"}
      ],
      "bindings": [
        {
          "port": "label",
          "mode": "ref",
          "source_node": "app_1",
          "source_port": "label"
        },
        {
          "port": "confidence",
          "mode": "ref",
          "source_node": "app_1",
          "source_port": "confidence"
        }
      ]
    }
  ],
  "edges": [
    {"source": "start_0", "target": "app_1"},
    {"source": "app_1", "target": "end_0"}
  ]
}
```

这张图含义：

```text
start_0 输出 query
app_1 的 text 输入引用 start_0.query
app_1 输出 label/confidence
end_0 收集 app_1.label 和 app_1.confidence
控制流 start_0 -> app_1 -> end_0
```

### 7.2 Render 后的关键 DSL 片段

假设测试 IDGen 生成：

```text
start_0 -> start::start0
app_1   -> application::application0
end_0   -> end::end0
```

`app_1` 会变成：

```json
{
  "id": "application::application0",
  "data": {
    "nodeMeta": {
      "nodeType": "application",
      "aliasName": "情感分类器"
    },
    "nodeParam": {
      "appId": "12345"
    },
    "inputs": [
      {
        "id": "in-text",
        "name": "text",
        "schema": {
          "type": "string",
          "value": {
            "type": "ref",
            "content": {
              "nodeId": "start::start0",
              "name": "query"
            }
          }
        }
      }
    ],
    "outputs": [
      {
        "id": "out-label",
        "name": "label",
        "schema": {"type": "string"}
      },
      {
        "id": "out-confidence",
        "name": "confidence",
        "schema": {"type": "number"}
      }
    ],
    "retryConfig": {
      "timeout": 60,
      "shouldRetry": false,
      "maxRetries": 0,
      "errorStrategy": 0,
      "customOutput": {}
    }
  }
}
```

边会变成：

```json
{
  "sourceNodeId": "start::start0",
  "targetNodeId": "application::application0",
  "sourceHandle": ""
}
```

---

## 8. ToIR：为什么还需要反向转换？

源码：`internal/dsl/clone.go`

`ToIR` 做的是：

```text
DSL -> IR
```

它不是执行引擎必需的，但对 Agent CLI 非常重要。

后续我们希望支持：

```bash
swf clone-ref --workflow-id 1588
```

这时系统会拿到已有工作流的 DSL，然后转回 IR，让 Agent 小步修改。

核心步骤：

```text
1. DSL ID 转可读 ID
   application::abc123 -> application_0

2. nodeParam 还原
   nodeParam.appId -> IR.AppID

3. inputs.schema.value 还原
   Value{type:"ref", content:{nodeId,name}}
   -> Binding{mode:"ref", source_node, source_port}

4. outputs 还原成 Port

5. DSL edge 还原成 IR edge
```

这体现项目设计里的“复用优先”：

```text
从零生成工作流很难。
先 clone 相似工作流，再改 IR，成功率更高。
```

---

## 9. M1 测试怎么读？

源码：`internal/dsl/dsl_test.go`

### 9.1 `TestRender_StartAppEnd`

验证最小链路：

```text
start -> application -> end
```

它检查：

- DSL 有 3 个节点。
- DSL 有 2 条边。
- application 节点 ID 正确。
- `IR.AppID` 渲染到 `nodeParam.appId`。
- `IR.Title` 渲染到 `nodeMeta.aliasName`。
- `app.text` 输入被渲染成 ref。
- ref 的 nodeID 已经从 `start_0` 转成 `start::start0`。
- retryConfig 默认值存在。
- edge 的 source/target 也转成 DSL ID。

### 9.2 `TestRender_LiteralAndInferredMode`

验证 `resolvedMode()`：

```go
{Port: "p_lit", Value: "hello"}
```

没有 mode，没有 source_node，所以推断为 literal。

```go
{Port: "p_ref", SourceNode: "start_0", SourcePort: "q"}
```

没有 mode，但有 source_node，所以推断为 ref。

### 9.3 `TestRender_DuplicateNodeID`

验证 IR 中不能有重复节点 ID。

重复 ID 会导致 `idMap` 不可靠，所以 render 直接报错。

### 9.4 `TestRoundTrip_RenderThenToIR`

验证：

```text
IR -> DSL -> IR
```

不会丢掉关键结构：

- 节点数量
- 边数量
- application 节点
- app_id
- binding
- outputs

### 9.5 `TestBuildDeps_*`

虽然 `deps.go` 后面在 M3/M4 更重要，但 M1 已经放了基础测试。

它验证：

```text
执行依赖 = edges ∪ ref bindings
```

也就是说，就算没有显式 edge，只要 B 的输入引用了 A 的输出，B 就必须依赖 A。

---

## 10. IR 与 DSL 字段映射表

| IR 字段 | DSL 字段 | 说明 |
|---|---|---|
| `Node.ID` | `DSLNode.ID` | 通过 `idMap` 从可读 ID 转执行态 ID |
| `Node.Kind` | `NodeMeta.NodeType` | 节点类型 |
| `Node.Title` | `NodeMeta.AliasName` | 展示名；为空时用 kind |
| `Node.AppID` | `NodeParam["appId"]` | application 节点专用 |
| `Node.DatasetID` | `NodeParam["datasetId"]` | dataset 节点专用 |
| `Node.WorkflowID` | `NodeParam["workflowId"]` | workflow 节点专用 |
| `Node.Params` | `NodeParam` | 节点私有参数 |
| `Node.Inputs` | `NodeData.Inputs` | 输入端口声明 |
| `Node.Outputs` | `NodeData.Outputs` | 输出端口声明 |
| `Binding` | `InputItem.Schema.Value` | 扁平 binding 被内联 |
| `Binding.Mode` | `Value.Type` | `ref/literal` |
| `Binding.SourceNode` | `RefContent.NodeID` | 先通过 idMap 转 DSL ID |
| `Binding.SourcePort` | `RefContent.Name` | 输出端口名或路径 |
| `Binding.Value` | `Value.Content` | literal 值 |
| `Edge.Source` | `DSLEdge.SourceNodeID` | 通过 idMap 转 DSL ID |
| `Edge.Target` | `DSLEdge.TargetNodeID` | 通过 idMap 转 DSL ID |
| `Edge.SourcePort` | `DSLEdge.SourceHandle` | condition 分支句柄 |
| `Layout` | 无 | 纯视图数据，不进入 DSL |
| `Meta` | 无 | DSL 当前不携带 meta，由 workflow 表保存 |

---

## 11. 阅读 M1 代码时的心智模型

你可以用编译器类比：

```text
IR       = 源代码 AST，适合人和 Agent 编辑
Renderer = 编译器
DSL      = 编译后的中间码/执行计划输入
ToIR     = 反编译器，用于 clone-ref
```

也可以用运行视角类比：

```text
IR 关心：我想搭什么图，哪个端口接哪里
DSL 关心：引擎执行时，每个节点完整配置是什么
```

所以 M1 不是在做执行引擎，而是在定义“工作流到底是什么”。

M1 完成后，后面的模块才能建立在它之上：

```text
M2 存储：把 DSL 存入 MySQL JSON 列
M3 校验：检查 IR 的图结构、端口、绑定、依赖
M4 执行：从 DSL 构建 Plan 并调度运行
```

---

## 12. 当前 M1 设计的边界

当前 M1 已经完成：

- IR 结构
- DSL 结构
- IR -> DSL render
- DSL -> IR clone
- 基础依赖构建
- 表驱动测试

但 M1 不做：

- 不做完整 validate。
- 不做节点执行。
- 不做 MySQL 存储。
- 不做能力发现。
- 不做真实 app-schema 拉取。
- 不做 UI 画布。

这些分别在 M2/M3/M4/M8 等里程碑实现。

---

## 13. 最后用一句话总结

```text
IR 的 Binding 用 mode 表示“怎么取值”，用 source_node/source_port 表示“ref 从哪里取值”。
ref 表示运行时引用上游输出，literal 表示工作流定义里的固定常量。
render 是 IR -> DSL 的编译过程，把可编辑结构转换成可执行结构。
```

