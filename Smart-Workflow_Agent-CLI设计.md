# Smart-Workflow 面向 Agent 的 CLI 设计

> 本文是 Smart-Workflow（Go 实现）面向 Agent 的命令行设计蓝图。
> 核心来源：Byteval 的 `workflow builder` skill（能力发现 + 增量构建 + 验证 + 可视化确认）与 PaiFlow 的 `node_debug`（单节点动态调试）。
> 每个设计点都标注来源：`[抄 Byteval]` / `[抄 PaiFlow]` / `[新增]`，便于开发溯源与答辩。

---

## 1. 设计目标

让 Agent（或人）通过命令行，把一句自然语言需求，转化为**可复用、可验证、可上传、可迭代**的工作流 DSL，并能在完成后**有效判断工作流是否有效**。

核心判断（贯穿全文）：

```text
Agent 构建工作流最可靠的方式不是"从零拼 JSON"，而是：
  1. 先发现能力（有什么可用）
  2. 优先复用（clone 相似工作流再改）
  3. 增量构建（小步 add/bind，每步可校验）
  4. 三层验证（静态 validate → 单点 node-debug → 端到端 run）
  5. 可视化确认（upload 草稿 → 画布看图 → 反馈迭代）
```

---

## 2. 三大支柱

### 2.1 能力发现 —— 把"猜"变成"选" `[抄 Byteval]`

Agent 无法凭空知道"有哪些节点、每个节点什么字段、这个端口该接哪"。用三个递进命令解决：

| 命令 | 解决的问题 | 来源 |
|---|---|---|
| `search` | 我**能用什么**能力？（搜 application / workflow / dataset） | 抄 Byteval `search` |
| `app-schema` | 这个能力**长什么样**？（拉取并缓存 input/output schema） | 抄 Byteval `app-schema` |
| `scope` | 这个输入端口**能接哪些上游输出**？（按连通性+类型过滤候选） | 抄 Byteval `scope` |

递进关系：

```text
search      → 从无到有：发现可用能力
app-schema  → 拿到契约：能力的输入输出规格
scope       → 收敛绑定：把"这个 prompt 接哪"从开放问题变成"从候选里选"
```

> `scope` 是最精妙的一环。它按"上游连通性 + ValueType"过滤出**合法的可绑定端口列表**，Agent 只需从列表里选语义正确的那个，而不是在整张图里猜。渲染层**不做**自动端口匹配，先 `scope` 收敛，再由 Agent `bind`。

### 2.2 增量构建 —— IR/DSL 两层 + 复用优先 `[抄 Byteval]`

**IR 与 DSL 分离**：Agent 操作 `ir.json`（易改的中间表示），`preview`/`upload` 时才渲染成 `dsl.json`（平台格式）。

session 目录结构：

```text
${SWF_SESSIONS_DIR:-$HOME/tmp/swf/sessions}/<sid>/
├── ir.json              # 中间表示（Agent 编辑对象）
├── meta.json            # 元信息（名称/项目/来源）
├── dsl.json             # 渲染出的最终 DSL
└── app_cache/<id>.json  # 缓存的 app schema
```

**复用优先，从零兜底**（关键策略）：

```text
相似 workflow 优先 clone 到 IR 后二次修改，从零搭建只是兜底方案。
改比写容易 —— 这直接降低 NL→DSL 的难度。
```

构建策略五选一：

```text
完全复用   → 已有工作流直接满足，返回/引用即可
复制微调   → 拓扑高度相似，改应用/输入/输出字段（推荐，clone-ref）
引用子流   → 已有工作流能完成某能力段，作为子工作流引用
参考拓扑   → 结构相似但 schema/项目不适配，只参考节点顺序
从零搭建   → 无相似工作流，或改造成本高于新建（兜底）
```

### 2.3 三层验证 —— 静态 / 单点 / 端到端 `[抄 Byteval + 抄 PaiFlow]`

这是"Agent 能否看出工作流是否有效"的完整答案。三层互补：

| 层 | 命令 | 特点 | 来源 |
|---|---|---|---|
| 静态校验 | `validate` | 不运行就查 30+ 类问题，免费、秒级 | 抄 Byteval `validate` |
| 单点调试 | `node-debug` | 单节点真跑一次，看输出/耗时/token | 抄 PaiFlow `node_debug` |
| 端到端 | `run` | 整图真跑，看最终结果是否达标 | 抄 PaiFlow `/api/workflow/chat` |

```text
validate(静态)   → 图对不对、绑定全不全、引用通不通    ← 先过这关（免费）
node-debug(动态) → 某节点真跑，输出/耗时/token 对不对  ← 再验单点（花 token）
run(端到端)      → 整图真跑，最终结果达标吗            ← 最后验全局
```

---

## 3. CLI 命令全集

入口：

```bash
swf <group> <subcommand> [flags]
```

统一 JSON envelope `[抄 Byteval]`（所有命令输出，Agent 可直接解析）：

```json
{ "ok": true,  "data": { } }
{ "ok": false, "error": { "code": "...", "message": "...", "details": { } } }
```

### 3.1 能力发现组 `[抄 Byteval]`

```bash
# 发现有哪些能力可用
swf search --kind application --name "qwen" --project-id 6970
swf search --kind workflow --name "文生图"     # 复用优先：先搜可复用工作流
swf search --kind dataset --name "情感评测"

# 拉取并缓存某能力的输入输出 schema
swf app-schema --sid "$SID" --app-id 12345 --project-id 6970

# 列出某端口此刻可绑定的上游候选（按连通性+类型过滤）
swf scope --sid "$SID" --node-id <node_id> --port prompt
```

### 3.2 增量构建组 `[抄 Byteval]`

```bash
# 建 session（空 / 从现有 DSL 导入）
swf init --name "情感评测" --project-id 6970
swf init --from-dsl ./existing.json

# 复用优先：克隆相似工作流到 IR
swf clone-ref --workflow-id 1588

# 小步编辑
swf add-node --sid "$SID" --kind application --app-id 12345 --title "Qwen"
swf add-edge --sid "$SID" --source start_0 --target <node_id>
swf bind --sid "$SID" --node-id <node_id> --port prompt \
  --mode ref --source-node start_0 --source-port query
swf remove-node / remove-edge / remove-port ...

# 声明式批量建图（Agent 更擅长生成完整声明）
swf plan schema                     # 打印 plan.json 规格，防字段拼错
swf plan apply -f plan.json         # 一次性建整张图
```

### 3.3 验证评测组 `[抄 Byteval + 抄 PaiFlow]`

```bash
# 静态校验（30+ 类，返回 error/warning），必须 0 error
swf validate --sid "$SID"

# 单节点隔离调试（抄 PaiFlow node_debug，调试时关闭重试拿即时反馈）
swf node-debug --sid "$SID" --node-id <node_id>

# 渲染 IR→DSL，打印摘要
swf preview --sid "$SID" --print-full

# 整图端到端运行
swf run --workflow-id <id> --input '{"query":"hello"}'
swf run --workflow-id <id> --input '{...}' --stream   # SSE 流式看每步
```

### 3.4 发布迭代组 `[抄 Byteval]`

```bash
# 上传草稿（默认创建新副本；覆盖已有需 --update-id 且强确认）
swf upload --sid "$SID" --description "情感评测工作流 - Agent Draft"
swf upload --sid "$SID" --update-id 1588 --description "..."   # 覆盖更新，需确认
```

---

## 4. `node-debug` 返回结构（评测信号核心）`[抄 PaiFlow]`

对标 PaiFlow 的 `NodeDebugRespVo`，再加一层断言 `[新增]`：

```json
{
  "ok": true,
  "data": {
    "node_id": "llm::x",
    "node_type": "llm",
    "status": "success",
    "input": {"prompt": "总结这段话"},
    "raw_output": "这段话讲了...",
    "output": {"summary": "这段话讲了..."},
    "exec_cost_sec": 1.23,
    "token_cost": {"prompt_tokens": 50, "completion_tokens": 20, "total_tokens": 70},
    "assertions": [
      {"type": "output_not_empty", "pass": true},
      {"type": "schema_valid", "pass": true},
      {"type": "cost_under_sec", "target": 3, "pass": true}
    ]
  }
}
```

Agent 据此自动判断：`status` 通不通、`output` 符不符合预期、`raw_output` vs `output` 有无信息丢失、`exec_cost_sec`/`token_cost` 成本可否接受。

> 关键细节：调试时**关闭节点重试**（`should_retry=false`），以拿到即时真实结果，不被重试掩盖问题。此细节来自 PaiFlow `flow_service.node_debug`。

---

## 5. `validate` 检查项清单 `[抄 Byteval]`

分 `error` / `warning` 两级。Agent 必须修复所有 error，再按 warning 检查连线语义。

```text
【图结构】
  Start/End 数量、重复节点、重复边、自环、环路、坏边、
  Start 入边、End 出边、不可达节点、到不了 End 的分支、传递冗余边
【节点关键字段】
  Application appID、Workflow workflowID、Dataset datasetID、缺少 app schema
【输入绑定】
  必填未绑定、必填仍是 placeholder、可选未绑定、
  raw 必填未绑定、raw 输入名不在缓存 schema 中
【数据引用】
  引用缺失节点/端口、Ref 类型不匹配、Ref 不是目标上游、
  End 收集非上游、相似端口名建议、多上游同名端口歧义
【Literal / schema-ref】
  Literal 类型可疑、系统模板放在非字符串端口、schema-ref 子字段缺失或类型未知
【Condition】
  节点缺 branches、分支无 conditions、表达式缺 comparator、分支缺 outgoing 边
【LLM 语义辅助（warning）】
  未使用输出、控制边缺对应数据依赖、分支未汇入 End
```

---

## 6. NL→DSL 完整闭环 `[抄 Byteval interaction-guide]`

```text
自然语言需求
  │
  ├─ 1. 意图归纳（能力链路 / 输入源 / 输出目标 / 运行方式）
  ├─ 2. search workflow      ← 先找可复用的完整/子工作流
  ├─ 3. 决定策略（完全复用/复制微调/引用子流/参考拓扑/从零）
  ├─ 4. search application    ← 只为缺失的能力槽位搜
  ├─ 5. ASCII Brief 给用户确认业务拓扑 ★
  ├─ 6. init / clone-ref 建 session
  ├─ 7. app-schema 拉真实 schema 并缓存
  ├─ 8. scope + bind 自动绑定端口
  ├─ 9. schema 不兼容 → 动态补 Python 胶水节点
  ├─ 10. validate 修到 0 error ★
  ├─ 11. upload 可视化草稿 → 用户到画布 UI 确认 ★
  └─ 12. 二次 update 迭代
```

### 6.1 职责边界：ASCII Brief `[抄 Byteval]`

**用户确认业务拓扑，Agent 处理技术绑定。** Brief 用 ASCII 图（不展示 YAML/ASL/端口绑定）：

```text
我理解你想搭一个"评测集文本 -> 情感分类 -> 结果导出"的工作流。

[工作流] 情感评测
评测集 21610/v1
  -> 读取评测集明细
  -> 文本解析（提取 question）
  -> 情感分类器
  -> End(label, confidence)

需要你确认：
1. 输入来自评测集 21610 吗？
2. 分类器推荐应用是否正确？
3. 导出字段是否为 label + confidence？

我会自动处理：真实 schema 拉取、字段绑定、batch、validate 修复。
```

只在以下情况才让用户确认绑定：多候选同名、类型兼容但业务含义不确定、需从复杂对象选字段、系统模板/敏感 literal 影响输出。

### 6.2 胶水节点 `[抄 Byteval]`

应用间 schema 不兼容时，Agent 动态插入 Python 转换节点：

```text
常见胶水：字段提取 / 字段重命名 / 数组包装 / 数组展开 /
         结果合并 / 类型转换 / 默认值补齐 / 过滤清洗

处理顺序：先搜已有转换应用 → 找不到再建 Python 应用 →
         用户确认转换意图（不要求确认代码细节）→ 拉 schema → 插入 → 重新 validate
```

### 6.3 画布的定位：确认层，不是编辑层 `[抄 Byteval]`

```text
图结构很难只在对话框里确认。
CLI 负责生成 + 校验（validate 0 error），
upload 成草稿后，用户在画布 UI"看图确认"，反馈后 Agent 再 update。
画布是"可视化确认层"，不是"必须的编辑入口"。
```

这回答了"不做画布怎么验证"——画布与 CLI 分工，不是二选一。

---

## 7. 一个可复制的最小示例

```bash
# 0. 建 session
SID=$(swf init --name "情感评测" --project-id 6970 | jq -r '.data.sid')

# 1. 能力发现
swf search --kind application --name "情感分类" --project-id 6970
swf app-schema --sid "$SID" --app-id 12345 --project-id 6970

# 2. 增量构建
swf add-node --sid "$SID" --kind application --app-id 12345 --title "情感分类器"
swf add-edge --sid "$SID" --source start_0 --target app_1
swf add-edge --sid "$SID" --source app_1 --target end_0

# 3. scope 收敛候选，再 bind
swf scope --sid "$SID" --node-id app_1 --port text
swf bind --sid "$SID" --node-id app_1 --port text \
  --mode ref --source-node start_0 --source-port query
swf bind --sid "$SID" --node-id end_0 --port label \
  --mode ref --source-node app_1 --source-port label

# 4. 三层验证
swf validate --sid "$SID"                       # 静态，必须 0 error
swf node-debug --sid "$SID" --node-id app_1     # 单点，看 output/token
swf preview --sid "$SID" --print-full

# 5. 上传草稿供画布确认
swf upload --sid "$SID" --description "情感评测工作流 - Agent Draft"

# 6. 端到端运行验证
swf run --workflow-id <uploaded_id> --input '{"query":"这家餐厅太棒了"}'
```

---

## 8. 与 PaiFlow / Byteval 的对齐总表

| 能力 | 来源 | 说明 |
|---|---|---|
| `search` 能力发现 | Byteval | 搜 app/workflow/dataset |
| `app-schema` 拉契约 | Byteval | 缓存到 `app_cache/` |
| `scope` 绑定收敛 | Byteval | 按连通性+ValueType 过滤候选（把猜变选） |
| IR/DSL 两层 session | Byteval | Agent 改 IR，渲染出 DSL |
| 复用优先 + clone-ref | Byteval | 改比写容易 |
| `plan apply` 声明式 | Byteval | Agent 擅长生成完整声明 |
| `validate` 30+ 检查 | Byteval | 静态、免费、秒级 |
| ASCII Brief 职责边界 | Byteval | 用户确认拓扑，Agent 管绑定 |
| 胶水节点 | Byteval | Python 转换解决 schema 不兼容 |
| upload 草稿→画布确认 | Byteval | 画布是确认层 |
| `node-debug` 单点调试 | PaiFlow | 返回 in/raw_out/out/cost/token，关重试拿即时反馈 |
| `run` + SSE 端到端 | PaiFlow | 整图运行看最终结果 |
| `assertions` 断言 | 新增 | 在 node-debug 上加自动判定 |

---

## 9. 落地优先级

```text
P0（评测闭环核心，先做）：
  init / add-node / add-edge / bind / validate / node-debug / preview
P1（能力发现，让 Agent 能自主构建）：
  search / app-schema / scope
P2（复用与批量，提升成功率）：
  clone-ref / plan apply / plan schema
P3（发布与迭代）：
  upload / run --stream / 画布可视化确认
```

> 一句话：先把 `构建 → validate → node-debug` 的最小评测闭环打通，再补能力发现和复用，最后接画布确认。

---

## 10. IR / DSL 数据结构设计

参考 PaiFlow 的执行态 DSL（`core/workflow/engine/entities/workflow_dsl.py`）与 Byteval 的编辑态 plan（builder `plan schema`），Smart-Workflow 采用**两层结构**。

### 10.1 分层原则

```text
IR (Intermediate Representation) —— 编辑态，Agent 操作它
  扁平 / 关系与结构分离 / 字段最小 / 容忍半成品 / 可读 ID / 视图与执行分离
        │
        │  preview 渲染（编译器：生成真 ID → 补 schema → 内联绑定 → 转边）
        ▼
DSL (Domain Specific Language) —— 执行态，引擎与存储只认它
  嵌套 / 字段完整 / 严格校验 / type::uuid ID / 直接可执行
```

关键差异：**PaiFlow 是"JSON 即 DSL"一层到底（靠人操作画布屏蔽复杂度）；Smart-Workflow 因主打 Agent CLI，多一层 IR 作为"源代码"，`preview` 是"编译器"。** 存储与执行只认 DSL。

### 10.2 IR 结构（整改后，Go struct）

> 本结构已吸收第 11 节的 5 条批判整改：执行依赖合并、字段合法性校验、端口声明、视图分离、作用域预留。

```go
type IR struct {
    Meta   Meta               `json:"meta"`
    Nodes  []Node             `json:"nodes"`
    Edges  []Edge             `json:"edges"`
    Layout map[string]NodePos `json:"layout,omitempty"` // 改④：视图与执行分离，纯 API 生成可不填
}

type Meta struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    ProjectID   string `json:"project_id,omitempty"`
    Source      string `json:"source,omitempty"` // clone-ref 来源工作流ID
}

type Node struct {
    ID         string         `json:"id"`               // 可读ID: start_0 / app_1
    Kind       string         `json:"kind"`             // start/end/application/dataset/workflow/condition/code
    Title      string         `json:"title,omitempty"`
    AppID      string         `json:"app_id,omitempty"`
    DatasetID  string         `json:"dataset_id,omitempty"`
    WorkflowID string         `json:"workflow_id,omitempty"`
    Inputs     []Port         `json:"inputs,omitempty"`   // 改③：端口声明，来自 app-schema
    Outputs    []Port         `json:"outputs,omitempty"`  // 改③：静态连线校验的地基
    Bindings   []Binding      `json:"bindings,omitempty"`
    Branches   []Branch       `json:"branches,omitempty"` // condition 专用
    Batch      *Batch         `json:"batch,omitempty"`
    Params     map[string]any `json:"params,omitempty"`   // 节点私有参数(code/prompt/template)
    // 注意：position 已移除 → 见 IR.Layout
}

type Port struct {
    Name     string `json:"name"`
    Type     string `json:"type"`     // string/integer/number/boolean/array/object
    Required bool   `json:"required,omitempty"`
}

type Binding struct {
    Port       string `json:"port"`
    Mode       string `json:"mode,omitempty"`        // ref/literal/clear，缺省推断
    SourceNode string `json:"source_node,omitempty"`
    SourcePort string `json:"source_port,omitempty"`
    Scope      string `json:"scope,omitempty"`        // 改⑤预留：""=当前, parent=外层, root=顶层
    Value      any    `json:"value,omitempty"`        // literal 用
    ValueType  string `json:"value_type,omitempty"`
}

type Edge struct {
    Source     string `json:"source"`
    Target     string `json:"target"`
    SourcePort string `json:"source_port,omitempty"` // condition 分支序号
}

type Branch struct {
    Index      int         `json:"index"`
    Conditions []Condition `json:"conditions"`
}

type Condition struct {
    LeftNode   string `json:"left_node"`
    LeftPort   string `json:"left_port"`
    Comparator string `json:"comparator"` // eq/ne/gt/gte/lt/lte/contains
    Right      any    `json:"right"`
    RightMode  string `json:"right_mode,omitempty"` // literal/ref
}

type Batch struct {
    Enable     bool   `json:"enable"`
    SourceNode string `json:"source_node"`
    SourcePort string `json:"source_port"`
    ItemName   string `json:"item_name,omitempty"`
    Size       int    `json:"size,omitempty"`
}

type NodePos struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}
```

IR 的四个"编辑友好"特征（对照 PaiFlow DSL 的"重"）：

```text
1. 扁平        binding 是一维 {port,mode,source_node,source_port}，无 5 层嵌套
2. 关系分离    bindings[] / edges[] 独立于节点结构，改连线不动节点内部
3. 字段最小    application 只需 id+kind+app_id，schema 由 app-schema 回填
4. 容忍半成品  加了节点没绑定、绑一半都合法，错误留给 validate 集中报
```

### 10.3 IR 示例（情感评测工作流）

```json
{
  "meta": { "name": "情感评测", "project_id": "6970" },
  "nodes": [
    { "id": "start_0", "kind": "start",
      "outputs": [ { "name": "query", "type": "string", "required": true } ] },
    { "id": "app_1", "kind": "application", "app_id": "12345", "title": "情感分类器",
      "inputs":  [ { "name": "text", "type": "string", "required": true } ],
      "outputs": [ { "name": "label", "type": "string" }, { "name": "confidence", "type": "number" } ],
      "bindings": [
        { "port": "text", "mode": "ref", "source_node": "start_0", "source_port": "query" }
      ] },
    { "id": "end_0", "kind": "end",
      "inputs": [ { "name": "label", "type": "string" }, { "name": "confidence", "type": "number" } ],
      "bindings": [
        { "port": "label",      "mode": "ref", "source_node": "app_1", "source_port": "label" },
        { "port": "confidence", "mode": "ref", "source_node": "app_1", "source_port": "confidence" }
      ] }
  ],
  "edges": [
    { "source": "start_0", "target": "app_1" },
    { "source": "app_1",   "target": "end_0" }
  ],
  "layout": {
    "start_0": { "x": 0,   "y": 0 },
    "app_1":   { "x": 300, "y": 0 },
    "end_0":   { "x": 600, "y": 0 }
  }
}
```

### 10.4 DSL 结构（执行态，对齐 PaiFlow）

字段命名刻意对齐 PaiFlow `workflow_dsl.py`，使 Smart-Workflow DSL 与 PaiFlow 高度兼容、可互导。

```go
type DSL struct {
    Nodes []Node `json:"nodes"`
    Edges []Edge `json:"edges"`
}

type Node struct {
    ID   string   `json:"id"` // application::a1b2c3  (type::uuid)
    Data NodeData `json:"data"`
}

type NodeData struct {
    NodeMeta    NodeMeta       `json:"nodeMeta"`
    NodeParam   map[string]any `json:"nodeParam"`
    Inputs      []InputItem    `json:"inputs"`
    Outputs     []OutputItem   `json:"outputs"`
    RetryConfig RetryConfig    `json:"retryConfig"` // 抄 PaiFlow：节点级容错
}

type NodeMeta struct {
    NodeType  string `json:"nodeType"`
    AliasName string `json:"aliasName"`
}

type InputItem struct {
    ID     string      `json:"id"`
    Name   string      `json:"name"`
    Schema InputSchema `json:"schema"`
}

type InputSchema struct {
    Type  string `json:"type"`
    Value Value  `json:"value"`
}

type Value struct {
    Type    string `json:"type"`    // ref / literal
    Content any    `json:"content"` // ref:{nodeId,name} ; literal:标量
}

type OutputItem struct {
    ID       string         `json:"id"`
    Name     string         `json:"name"`
    Schema   map[string]any `json:"schema"`
    Required bool           `json:"required"`
}

type Edge struct {
    SourceNodeID string `json:"sourceNodeId"`
    TargetNodeID string `json:"targetNodeId"`
    SourceHandle string `json:"sourceHandle"` // "" / fail / 分支序号
}

type RetryConfig struct {
    Timeout       float64        `json:"timeout"`
    ShouldRetry   bool           `json:"shouldRetry"`
    MaxRetries    int            `json:"maxRetries"`
    ErrorStrategy int            `json:"errorStrategy"` // Interrupted/FailBranch/CustomReturn
    CustomOutput  map[string]any `json:"customOutput"`
}
```

### 10.5 IR → DSL 渲染算法（preview 的核心）

```text
1. 生成真实 ID
   每个 IR 节点分配 <kind>::<uuid>；维护 idMap[ir.id]=dsl.id（app_1 → application::c4d2e9）
2. 展开节点结构
   按 kind 建 DSL 骨架；application/dataset/workflow 从 app_cache 读 schema，
   补全 inputs/outputs 的 name/type/id/required
3. 内联绑定（关系 → 结构）
   ref     → inputs[name].schema.value = {type:ref, content:{nodeId:idMap[src], name:src_port}}
   literal → schema.value = {type:literal, content:value}
   缺省 mode → 有 source_node 判 ref，否则 literal
4. 转换边
   ir.edge{source,target,source_port} →
   dsl.edge{sourceNodeId:idMap[src], targetNodeId:idMap[tgt], sourceHandle:source_port|""}
5. 附加参数与默认值
   nodeParam(appId/template/code)、retryConfig 默认值、branches → nodeParam.intentChains
6. 落库时 layout 另存
   引擎执行只读 nodes/edges；layout 可选存一份给画布回显
```

反向 `clone-ref`（DSL→IR）是逆操作：抽 name 丢 uuid、把内联 value 提成扁平 bindings、type::uuid 换回可读 ID。这是"复用优先"策略的落地基础。

### 10.6 执行依赖构建（改①，binding 即隐式依赖）

```text
deps = {}
for e in edges:            deps[e.target] += e.source     // 显式控制流
for n in nodes:
  for b in n.bindings:
    if b.mode == ref:      deps[n.id] += b.source_node    // 数据流 → 隐式控制流
拓扑排序基于 deps（去重）；检测到环则 validate 报 cycle
```

**这解决了"binding 引用了非 edge 上游节点，可能被提前调度"的隐患**：数据引用自动补一条执行依赖，B 引用 A 就意味着 A 是 B 的前驱。

### 10.7 两层字段对照

| 维度 | IR（编辑态） | DSL（执行态） |
|---|---|---|
| 节点 ID | `app_1`（可读） | `application::uuid`（PaiFlow 式） |
| 绑定 | 扁平 `bindings[]`，独立 | 内联进 `inputs[].schema.value` |
| 端口 schema | 声明式 `Port{name,type}` | 完整 `type/id/required` |
| 边 | `{source,target,source_port}` | `{sourceNodeId,targetNodeId,sourceHandle}` |
| 视图数据 | 独立 `layout` | 可选附带 |
| 半成品 | 允许 | 不允许 |
| 谁用 | Agent / CLI | 引擎 / 存储 / 画布 |
| 存储 | session `ir.json`（临时） | `flow.data`（落库、发布） |

---

## 11. IR 设计的权衡与批判回应

以下 5 条批判来自架构评审，逐条给出判定与处理。**结论：2 改结构、1 补校验、1 结构预留、1 澄清取舍。**

| # | 批判 | 判定 | 处理 |
|---|---|---|---|
| 1 | 隐式依赖陷阱（控制流/数据流分离） | 部分成立 | **改**：执行依赖 = edges ∪ bindings 推导的隐式边（见 10.6） |
| 2 | 胖节点 / 类型安全薄弱 | 取舍非缺陷 | **补校验**：结构保持扁平，validate 加 `field_not_allowed_for_kind` |
| 3 | 缺 Output 定义，无法静态连线校验 | 真缺陷 | **改**：IR 节点补 `inputs/outputs` 端口声明（见 10.2） |
| 4 | UI 污染执行域（Position 在 Node 上） | 成立 | **改**：`position` 剥离为平行 `layout` 表（见 10.2） |
| 5 | 作用域 / 上下文穿越（嵌套 Batch） | 未来问题 | **预留**：binding 加可选 `scope`，MVP 禁止跨层引用 |

### 11.1 逐条说明

**① 隐式依赖**：批判描述的"B 引用 A 但无 A→B 边，B 被提前调度"崩溃场景，在有 `validate` 的 `ref_not_upstream` 检查下本不该进执行期。但它戳中的真问题是双轨制的一致性负担。**采纳更优方案**：让数据依赖自动生成控制依赖（10.6），从根上杜绝提前调度。

**② 胖节点**：这是编辑态的**有意取舍**。IR 要频繁 JSON 序列化、跨语言读写、容忍半成品，扁平结构比 Go 多态（每 kind 一个 struct + 自定义 Unmarshal）更划算；几个空字符串指针的内存开销可忽略。类型安全弱的问题**用校验兜底**：validate 拦截 `kind=start` 却带 `app_id` 之类的脏数据。**结构不改，补校验。**

**③ 缺 Outputs**：**真漏洞，已认。** 原设计想"outputs 渲染时从 app_cache 补"，但这样编辑态（scope/validate）就没有端口信息可校验。补 `inputs/outputs` 端口声明后，`scope` 能校验 source_port 存在性、`validate` 能校验类型兼容，全部前移到保存阶段。

**④ UI 污染**：**成立。** 坐标是视图数据，放 Node 破坏单一职责，第三方 API 生成工作流被迫关心坐标。剥离为平行 `layout map[nodeID]NodePos` 后，纯 Agent/API 路径完全不碰 layout，执行域干净。这比 PaiFlow（position 挂节点上）更清晰，是我们的改进点。

**⑤ 作用域穿越**：批判深刻但属**未来问题**。当前 Batch 内引用"迭代项"是引用 `item_name`（Byteval 模型已覆盖），跨分支引用被可达性校验部分拦截。真正的软肋是一维 `source_node` 不携带作用域信息——这是所有扁平引用 IR 的共同难题（PaiFlow 靠 iteration 子图边界规避）。**MVP 不实现，但给 binding 预留 `scope` 字段**（""/parent/root），当前恒为空且禁止 Batch 跨层引用，未来做嵌套作用域时不必推翻结构。

### 11.2 整改带来的校验增强

新增/强化的 `validate` 检查项：

```text
[改①] cycle              —— 基于 edges∪bindings 的依赖图检测环
[改②] field_not_allowed_for_kind —— start 节点不得带 app_id/dataset_id/workflow_id 等
[改③] source_port_not_found      —— binding 的 source_port 不在上游 outputs 中
[改③] type_mismatch              —— source_port 类型与目标 input 类型不兼容
[改⑤] cross_scope_ref_forbidden  —— MVP 阶段 Batch 内禁止跨作用域引用
```

一句话：**这轮批判帮我把 IR 从"能用"推进到"经得起推敲"——补端口声明打通静态校验、拆 layout 净化执行域、合并依赖杜绝提前调度、预留 scope 不堵死未来。扁平胖节点是编辑态的合理取舍，用校验兜底而非改结构。**
