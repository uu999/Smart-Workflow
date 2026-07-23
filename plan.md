# Smart-Workflow 实施计划（plan.md）

> 配套文档：[Smart-Workflow_Agent-CLI设计.md](./Smart-Workflow_Agent-CLI设计.md)（设计蓝图，本文件是实施计划）
> 定位：**较完整的可用平台**（非一次性 MVP），含异步调度、Redis 缓存、较全节点类型、Python sidecar。
> 语言：Go 为主 + Python sidecar 执行用户代码。画布 MVP 阶段不做，CLI 优先。

---

## 0. 技术栈（已定型）

| 层 | 选型 | 用途 |
|---|---|---|
| 语言（主） | Go 1.22+ | API / 引擎 / CLI / 调度 |
| 语言（执行） | Python 3.9+ + FastAPI | sidecar，跑用户 code 节点（runner 仅用标准库） |
| HTTP 框架 | gin | Go API server |
| CLI 框架 | cobra + viper | `swf` 命令与配置 |
| DB 访问 | sqlc（已定） | 写 SQL 生成类型安全代码，JSON 列查询可控、性能好 |
| 数据库 | MySQL 8.0 | 元数据 + DSL（JSON 列，对齐 PaiFlow） |
| 迁移 | goose | schema 版本管理，可重复执行 |
| 缓存/运行态 | Redis 7 + go-redis v9 | 定义缓存、引擎缓存、运行态、锁 |
| 任务队列 | hibiken/asynq | 基于 Redis 的异步任务，跑长工作流 |
| 日志 | uber-go/zap | 结构化日志，带 run_id/node_id |
| JSON Schema | santhosh-tekuri/jsonschema | 节点输出校验（对标 PaiFlow do_validate） |
| 测试 | testify | 单测 |
| 部署 | docker-compose | 一键起 mysql+redis+server+worker+sidecar |
| 对象存储（二期） | MinIO | 大对象（文件/大文本），MVP 先不做 |

进程形态：

```text
swf-server    对外 gin API + 同步执行短任务
swf-worker    asynq 消费者，跑长工作流（可与 server 合一，二期拆分）
swf           cobra CLI 二进制
py-sidecar    FastAPI，Go 通过 HTTP 调它跑用户 Python code
```

---

## 1. 模块拆分（与设计文档对应）

```text
M-DSL       IR/DSL 数据结构 + 渲染器 + clone-ref 反渲染   ← 设计文档 §10
M-STORE     MySQL 存储层（workflow/version/run/node_run/application）
M-VALID     校验器（结构/字段/绑定/引用/环）              ← 设计文档 §11.2 检查项
M-VARPOOL   变量池（两层 map + 路径解析 + 输出校验）        ← 全项目核心
M-ENGINE    执行引擎（Builder + 依赖驱动 Scheduler）        ← 设计文档 §10.6
M-NODES     节点执行器（start/end/condition/http/llm/code）
M-SIDECAR   Python FastAPI sidecar（跑 code 节点）
M-API       gin HTTP API（资源 CRUD + run + node-debug）    ← 设计文档 §3
M-CLI       swf cobra CLI（能力发现/构建/验证/发布）         ← 设计文档 §3
M-CACHE     Redis 缓存层（定义缓存/引擎缓存/运行态/锁）
M-ASYNC     asynq 异步任务（长工作流入队执行）
M-CATALOG   能力发现（search/app-schema/scope）             ← 设计文档 §2.1
```

依赖关系（谁先谁后）：

```text
M-DSL ─┬─> M-VALID ─┐
       ├─> M-VARPOOL ├─> M-ENGINE ─> M-NODES ─> M-API ─> M-CLI
       └─> M-STORE ──┘                  │
                                   M-SIDECAR
       M-CACHE / M-ASYNC / M-CATALOG 在 API 之后并行补
```

---

## 2. 里程碑（按依赖排序，不定死天数）

### M0 · 工程基线
```text
交付：
  monorepo 骨架（cmd/ internal/ migrations/ deployments/）
  docker-compose：mysql + redis 起得来
  goose migration 可重复执行
  gin server 骨架 + healthz
  FastAPI sidecar 骨架 + /healthz
  zap 日志 + viper 配置 + 统一错误码/JSON envelope
验收：
  一条命令起全部服务；Go 能调通 sidecar /healthz；migration 幂等
```

### M1 · IR/DSL 数据结构与渲染（M-DSL）
```text
交付：
  IR structs（整改后，含 inputs/outputs/layout/scope 预留）
  DSL structs（对齐 PaiFlow 字段名）
  IR→DSL 渲染器（生成真ID→补schema→内联绑定→转边）
  DSL→IR 反渲染（clone-ref 基础）
  表驱动单测：一个 start→app→end 的 IR 能渲染成合法 DSL
验收：
  给定 §10.3 示例 IR，渲染出的 DSL 与 §10.4 结构一致
```

### M2 · 存储层（M-STORE）
```text
交付：
  表：workflow / workflow_version / workflow_run / node_run / application
  DSL 存 JSON 列（draft_dsl / release_data）
  软删除字段、乐观锁字段
  workflow CRUD + publish（生成 version 快照）
验收：
  能存草稿 DSL、能发布、能读回；version 不可变
```

### M3 · 变量池 + 校验器（M-VARPOOL + M-VALID）★核心
```text
交付：
  VariablePool：map[nodeID]map[port]any + RWMutex
  路径解析：result.segments[0].text（对标 PaiFlow VariablePoolTest）
  输出 JSON Schema 校验
  Validator：结构/字段合法性/绑定/引用/环（§11.2 全部检查项）
  执行依赖构建：edges ∪ bindings（§10.6）
验收：
  把 PaiFlow VariablePoolTest 用例翻成 Go 表驱动测试全过
  各类坏 IR（缺start/环/坏ref/字段越权）都被 validate 拦下
```

### M4 · 执行引擎 + 基础节点（M-ENGINE + M-NODES）
```text
交付：
  Builder：IR/DSL → ExecutablePlan（节点+依赖+执行器绑定）
  引擎入口设计为异步可复用纯函数：engine.Run(ctx, runID) error
    —— 不关心谁调它（同步 handler 直接调 / worker 队列调，同一入口）
    —— 状态、输入、输出全部落 workflow_run/node_run，不依赖调用方内存
    —— 避免 M9 加队列时重构调度入口
  Scheduler：依赖驱动 + goroutine 并发 + semaphore 限流 + context 超时/取消
  节点状态机：PENDING→RUNNING→SUCCESS/FAILED/SKIPPED，retry
  节点：start / end / condition / http
  node_run 落库（input/output/cost/error/attempt）
验收：
  跑通 start→http→end；condition 分支正确；node_run 有完整快照
  engine.Run 可被 M6 同步 handler 与 M9 worker 复用，无需改核心
```

### M5 · Python sidecar + code 节点（M-SIDECAR）✅ 已完成
```text
交付：
  FastAPI sidecar：POST /run/python-code
  进程隔离（子进程）+ 超时 kill + stdout/stderr 大小限制
    —— 注意：这是"进程隔离+超时"，非语言级/系统级真沙箱（见技术债 TD-5）
  Go code 节点执行器：HTTP 调 sidecar
验收：
  code 节点能跑 sink(data)；超时能被杀；异常标准化返回
落地说明：
  sidecar/runner.py：子进程入口，inputs 注入 + sink() 收集输出 +
    stdout/stderr 隔离进日志 + 结果走 dup 的 fd1，严格 JSON 序列化
  sidecar/main.py：subprocess 跑 runner，timeout/崩溃/坏输出各自错误码
  internal/engine/nodes/code.go：CodeExecutor 调 sidecar，SidecarURL 可注入
  测试：Go 侧 httptest mock（success/error/unreachable/bad-resp/registered）
    + 调度器 start→code→end + 真实 sidecar integration 冒烟
M5 反思后补强（已落地）：
  ① 节点执行器注册表由包级全局单例改为实例化 nodes.Registry
     （带 RWMutex，-race 通过；不再有并行测试互相污染）
  ② config 打通：engine.New(store, sidecarURL) 从 config.Sidecar.BaseURL
     注入 code 节点的 sidecar 地址，不再只靠环境变量兜底
  ③ Go↔Python 契约守卫：共享 golden fixture
     （sidecar/contract/code_run.golden.json）+ 两侧测试，
     字段改名会让 Go 与 Python 测试同时失败（已做负向验证）
```


### M6 · HTTP API + node-debug（M-API）✅ 已完成
```text
交付：
  资源 API：projects/workflows/applications/runs
  run（提交即返回 runID + GET /runs/{id} 轮询）
    —— M6 阶段可先在 handler 内直接调 engine.Run 跑完再落库，
       M9-a 再换成入队；因引擎已是 Run(ctx,runID) 纯函数，切换不改核心
  node-debug（单节点调试，返回 in/raw_out/out/cost/token）
  node-debug 关重试拿即时反馈（抄 PaiFlow）
  assertions 断言（新增）
验收：
  curl 完成 create→validate→run→node-debug 全链路
落地说明：
  internal/api：四类资源全 CRUD 路由（router.go），统一 httpx envelope，
    错误映射 helper（failFromErr：NotFound→404 / VersionLock→409 /
    InvalidJSON→400 / 其余→500）+ pageParams 分页保护
  run 异步模型：POST /v1/runs 先落 pending 记录，后台 goroutine 用独立
    context 调 engine.Run（不随请求取消），立即返回 {run_id, status:"pending"}；
    GET /v1/runs/{id} 轮询状态 + node_run 快照。M9 只需把 goroutine 换 asynq，
    HTTP 契约不变
  service 层：project/application/run 三 service + workflow 补 List/Delete；
    validate（DSL→ToIR→Validate）、node-debug（engine.DebugNode 关重试 + 3 断言）
  装配：cmd/server/main.go 接 mysql.Open + engine.New(store, cfg.Sidecar.BaseURL)
    → api.NewRouter，打通 config→server→engine→code→sidecar 全链路（清 TD-7）
  测试：api helpers 单测（错误码映射 + 分页）、service 纯逻辑单测
    （normalizeJSON / validateDSL / resolveVersion）、engine DebugNode 单测；
    E2E integration（-tags integration，真实 MySQL + mock sidecar）串起
    create→validate→publish→run→轮询 succeeded→node-debug，断言 node_run 落库
    与 404/400 错误码；Python 侧补 TD-6 脱敏行为断言（message 不含绝对路径、
    完整 traceback 留 logs）
  顺带清 TD-1（Python 版本声明对齐 3.9+）、TD-6（traceback 脱敏）、TD-7（真实装配）
反思后补强（已落地）：
  ① 后台 run 加固（TD-8）：裸 goroutine → api.RunDispatcher，
     panic 兜底落 failed + semaphore 限并发 + 优雅退出 drain，满载回 503
  ② version 语义消歧（TD-9）：createRunReq.Version 改 *int32，
     省略=最新发布 / -1=草稿 / N>0=指定 / 显式 0 或 < -1 报 400
  其余反思项记入技术债 TD-10（run 前 validation gate）、
  TD-11（handler 强耦合）、TD-12（列表/大对象/断言契约债），随 M7/M8 再清
```

### M7 · CLI（M-CLI）★卖点 ✅ 已完成
```text
交付：
  swf init/add-node/add-edge/bind/remove-node/remove-edge/remove-binding（增量构建）
  swf validate/preview/node-debug/run（验证评测）
  swf upload（发布，默认新副本；--update-id 覆盖需 --confirm）
  session 目录（ir.json/meta.json/dsl.json/app_cache）
  统一 JSON envelope + 结构化错误（code/message/hint）
验收：
  Agent 仅靠 CLI 完成：建图→bind→validate→node-debug→upload→run
落地说明：
  internal/cli：session（IR/DSL 两层会话，原子写落盘）+ output（envelope）
    + client（调服务端，透传 code/message/details）+ 三组 cobra 命令
  作用面决策（风险1）：validate/preview 在 CLI 进程内跑（复用 dsl.Renderer +
    validator，零成本离线），node-debug/run/upload 才碰服务端——"upload 前修到
    0 error"闭环得以成立，不必先灌半成品草稿进库
  cmd/swf/main.go：cobra 根命令，结构化错误已进 envelope 后据 error 设非零退出码
  测试：session 往返/端口解析单测、全离线建图闭环（init→…→validate→preview）、
    结构化错误、node-debug/run/upload 转发（httptest mock server）；-race 通过
M7 反思后先行去风险（已落地，见下）：
  ① 风险4：render/clone 从不序列化 condition.branches / batch → rendered
     condition 执行期必报 "has no branches"。已修：renderNode 按 ConditionExecutor
     读取格式（[]any{index,conditions}，left_node 经 idMap 译真实 ID）序列化
     branches+batch；ToIR 反向还原；补 IR→Render→Build→Run 端到端回归防线
     （证明 rendered condition 真能驱动分支剪枝）+ JSON 往返无损测试
  ② 风险2：application/llm 无执行器 → 决策 M7 验收样例限 http+code，
     application/llm 真实执行随 M8 app-schema/catalog 落（避免造假"通过"信号）
  ③ TD-10：run 前置 validation gate（见 TD-10 已修复条目）
  ④ 风险1：新增无状态 POST /v1/node-debug（吃渲染后 DSL 节点，不需 workflowID）
```

### M8 · 能力发现（M-CATALOG）✅ 已完成
```text
交付：
  swf search（application / workflow；dataset 明确 NOT_SUPPORTED，缓期 M10）
  swf app-schema（拉 schema → 解析端口 → 缓存 app_cache + 物化进 IR 节点）
  swf scope（按连通性 + 类型过滤可绑定候选，把"猜端口"变"选端口"）
验收：
  Agent 能先 search 再 app-schema 再 scope，把"猜端口"变"选端口"
落地说明：
  服务端 search：application.sql / workflow.sql 加 SearchApplications /
    SearchWorkflows（WHERE project_id=? AND name LIKE ?），sqlc 重生成；
    service 层 Search 方法 + likePattern（空=%匹配全部，转义 \ % _ 防通配注入）；
    API 层 list handler 感知 ?name= 存在即走 Search，否则原 List（契约兼容）
  schema 格式（internal/catalog）：input_schema/output_schema 约定为「端口数组」
    [{"name","type","required","desc"}]，type 用字符串，与 dsl.Port 一一对应；
    ParsePortList / ParseAppSchema 解析，空→nil、缺 name/type→error
  CLI：search 转发服务端；app-schema 拉详情→解析→WriteAppCache+回填节点端口；
    scope 进程内复用 dsl.BuildDeps 求传递上游 + 类型过滤，零成本离线
  测试：catalog 解析单测、likePattern 单测、CLI search/app-schema/scope
    httptest 单测（含 dataset NOT_SUPPORTED、端口物化进 IR、连通性+类型过滤）；
    -race 全绿、vet（含 -tags integration）干净
M8 反思后先行去风险（参考 Byteval workflow builder 实证，见下）：
  ① 风险1（承重）：设计 §10.5 字面说"render 读 app_cache 补端口"，但查 Byteval
     真实 .wb_sessions/*/ir.json —— 每个节点的 inputs/outputs 都内联在 IR 里，
     app_cache 只是记录。故决策：app-schema 把端口物化进 IR 节点（cache 为记录），
     render 无需重写、IR 自包含性保留。渲染层零改动
  ② 风险2（承重）：无 dataset 表 + 无节点执行器 → M8 search 只做 app+workflow，
     dataset 存储/执行器随 M10 "评测集→分类器" E2E 一起落，避免造空壳
  ③ 风险3（契约锁死）：app schema 无格式约定 → 采用端口数组（参考 Byteval 端口
     列表结构，但用 SWF 字符串 type 而非 PaiFlow 整数 value_type），与既有
     validator/render/scope 同一套类型词汇，避免 CLI 锁死难改的契约债（TD-12）
```

### M9-a · Redis 缓存 + 异步执行（M-CACHE + M-ASYNC）✅ 已完成
```text
交付：
  Redis：定义缓存 swf:def / 引擎缓存 swf:plan（键预留）/ 运行态 swf:run（键预留）/ 运行锁
  asynq：长工作流入队，swf-worker 消费；run 立即返回 runID
    —— 复用 M4 的 engine.Run(ctx, runID)，worker 只是它的调用方之一
  失败重试 + 并发闸门（worker 并发度可配）+ 优先级队列（调试优先/批量靠后）
  GET /runs/{id} 轮询状态（pending/running/succeeded/failed）
验收：
  长任务异步跑不卡请求；服务重启后 worker 能捡回在途任务；
  引擎缓存命中省重复构建；轮询能拿到运行状态
落地说明：
  internal/cache：Store 接口（GetBytes/SetBytes/Del）+ RedisStore（go-redis v9）
    + MemStore（内存实现，供单测无需真 Redis）；键集中在 keys.go（swf:def/plan/run）
  internal/async：TypeRunWorkflow 任务（payload 只带 runID，状态全在 DB）；
    Enqueuer（asynq client，TaskID(runID) 去重＝运行锁，MaxRetry 重试）；
    RunProcessor（asynq.Handler：解 payload → engine.Run；坏 payload SkipRetry，
    业务失败正常重试）；优先级队列 debug>default>batch（QueueWeights 权重）；
    Submitter（实现 api.RunSubmitter：Submit=入队，Shutdown=关 client）；
    NewServer/NewMux（worker 端装配）
  API 解耦：抽 RunSubmitter 接口 { Submit(runID)bool; Shutdown(ctx)error }，
    进程内 RunDispatcher（M6 兜底）与 async.Submitter（M9-a）双实现，
    createRun handler 零改动（编译期断言两实现都满足接口）
  引擎缓存：engine.WithCache 可选注入 DSLCache；loadDSL 对已发布版本先查
    swf:def:{wf}:{ver}（命中省 MySQL 读，TTL 1h），草稿(-1)可变不缓存；
    nil cache 时行为与之前完全一致（现有测试不受影响）
  装配：cmd/server 按 config.Redis.Addr 是否配置切 asynq/dispatcher + 启用缓存；
    cmd/worker 新增（asynq server 消费三队列 + engine + 优雅退出）；
    Makefile 加 run-worker；docker-compose 已含 redis
  测试：cache round-trip/TTL/键、async payload/任务选项/processor（成功/重试/
    SkipRetry）、engine 缓存命中与草稿不缓存；-race 全绿、vet（含 integration）干净
范围界定（诚实说明）：
  - 运行态查询：GET /runs/{id} 仍以 MySQL 为权威源（已满足验收）；swf:run 键已预留，
    Redis 状态镜像作为后续读快路径的挂点，本阶段不引入一致性负担
  - 引擎缓存：本阶段落 swf:def（DSL 缓存）；swf:plan（构建好的 Plan 缓存）键已预留，
    因 builder.Plan 含函数式执行器绑定不宜直接 JSON 序列化，留待需要时以 DSL 重建
  - "重启捡回在途任务"：asynq 任务持久在 Redis，worker 重启自动续跑，无需额外代码
  - 未做容器化 server/worker 镜像（无 Dockerfile），worker 以二进制/`make run-worker` 运行
```

### M9-b · SSE 流式推送（M-CACHE 事件流）✅ 已完成
```text
交付：
  运行事件写 Redis Stream：swf:run:{id}:events
  SSE 接口：GET /runs/{id}/events，推 node_start/node_end/run_end 事件
  swf run --stream：CLI 订阅并打印每步（对标 PaiFlow /api/workflow/chat）
验收：
  run --stream 实时看到每个节点的状态流转与输出，无需轮询
反思决策（承重项，先复盘再落）：
  ① 风险1（跨进程）：事件产生在执行侧，SSE 在 server 侧——asynq 模式下 engine.Run
     跑在 worker 进程，内存 channel 传不到 server。决策：内存+Redis 双路径——
     dispatcher 模式用进程内 MemHub，asynq 模式用 Redis Stream 跨进程
  ② 风险2（不耦合 Redis）：scheduler 是纯执行核心，绝不能 import redis。决策：
     注入 runevent.Emitter 接口（nil→NopEmitter 零开销），engine 不感知事件落到哪
  ③ 风险3（契约锁死）：事件 schema 若直接复用 node_run 落库字段会被 DB 绑死。
     决策：独立叶子包 runevent（零内部依赖），CostMs 用毫秒整数、Status 用字符串，
     面向 --stream/前端画布稳定，与落库字段解耦
  ④ 风险4（缺 node_start）：只发结束事件则画布只能看到跳变。决策：调度 loop 派发
     节点前发 node_start（单线程内分配单调 seq，有序）
  ⑤ 风险5（生命周期）：SSE 不收敛会泄漏。决策：run_end 关 channel + ctx.Done 退订，
     且 run_end 落库后再发（收到即可 GET /runs/{id} 读到终态）
  ⑥ 风险6（无 Redis 时 --stream）：dispatcher 模式没有 Stream。决策：MemHub 兼作
     Emitter+Source；未装配事件源时 SSE 回 501，CLI 自动回退轮询
落地说明：
  internal/runevent：叶子包，RunEvent DTO + Emitter 接口 + NopEmitter；
    Phase 常量 node_start/node_end/run_end；NowMillis 生成 TS
  scheduler/engine：Options.Emitter 注入；coord.emit 在单线程 loop 内分配 seq 并
    发 node_start（派发前）/node_end（终态回收时）；engine.WithEmitter 注入，
    persistFinal 成功后 emitRunEnd（落库后再发，保证终态可读）
  internal/eventbus：Source 订阅抽象；MemHub（进程内 pub/sub，非阻塞 fan-out、
    满则丢中间事件、run_end 关闭并清理、ctx 退订）；RedisEmitter（异步缓冲 XADD、
    单 drain 保 FIFO、run_end 尽力送达并设 TTL）；RedisSource（XREAD 阻塞转 channel、
    从流头读补齐历史、遇 run_end 关闭）；StreamKey=cache.RunKey(id)+":events"
  API：GET /v1/runs/:id/events（gin c.Stream）；Deps.EventSource 注入；无源回 501；
    迟到订阅（run 已终态）直接回放 node_run 快照+合成 run_end；15s 心跳兼终态兜底
  CLI：swf run --stream 订阅 SSE 逐行打 NDJSON envelope，见 run_end 收流再拉终态；
    501/连接失败自动回退 pollRun（任何部署形态都能拿到终态）
  装配：cmd/server dispatcher 模式共享一个 MemHub 作 engine Emitter + SSE Source；
    asynq 模式 server 用 RedisSource、worker 用 RedisEmitter（cache.NewRedisClient 共享配置）
  测试：eventbus（MemHub 发布/run_end 关闭/多订阅者/隔离/退订/慢消费不阻塞、
    Redis 侧编译期接口断言、StreamKey）；api（501 回退、SSE 帧格式、终态回放、
    终态判定）；cli（流式消费+终态、501 回退轮询）；-race 全绿、vet（含 integration）干净
范围界定（诚实说明）：
  - 断点续传：RunEvent.Seq 已随 SSE id 下发、Redis message id 天然有序，但客户端
    未实现 Last-Event-ID 重连补发；重连当前从流头重放（已发布事件 Redis 保留 6h）
  - process 事件：设计文档提到 node "process" 中间态，本阶段落 node_start/node_end/
    run_end 三相已满足"看每步状态流转+输出"验收；节点内进度（如 LLM token 流）
    需执行器透出，留待有需求时扩 Phase
  - 背压策略：MemHub/RedisEmitter 缓冲满时丢中间事件（SSE 是尽力而为的实时视图，
    权威状态在 MySQL），run_end 例外——尽力送达以保证客户端收敛
  - 鉴权/限流：SSE 端点复用现有中间件，未额外加长连接数配额，随 M10 加固一并处理
```

### M10 · 端到端样例 + 加固
```text
交付：
  E2E 样例：评测集(dataset)→分类器(application)→结果落地(用户在图内接「结果合并/写表」节点)
  clone-ref + plan apply（复用与批量实例化）
  可选鉴权 API Key + 限流 + 优雅退出（已有）
  全容器化部署（server/worker/sidecar Dockerfile + compose）+ README
验收：
  新机器按 README 30 分钟起（docker compose 一键起五件套）；样例一键跑通；失败有可读错误
落地进度（12/12 ✅ M10 完成）：
  #26-#30 dataset 存储/执行器 DI/DatasetExecutor/ApplicationExecutor/batchWrap  ✅
  #32 clone-ref  ✅   #33 plan-apply(声明式建图)+plan-schema  ✅
  #34 可选鉴权 API Key + 限流(healthz/SSE 豁免，空配置放行)  ✅
  #35 全容器化  ✅ —— 4 Dockerfile(server/worker/swf 多阶段静态→alpine；sidecar python:3.12-slim
     绑 0.0.0.0) + migrate 一次性服务(goose v3.27.2 烘 migrations，depends mysql healthy 跑完即退) +
     compose 补 5 服务(server/worker 经 SWF_* 注入容器内地址 mysql:3306/redis:6379/sidecar:8090，
     depends migrate completed) + .dockerignore(排除 .venv 防 arm64 二进制污染) + Makefile up-all/docker-build
  #36 E2E 样例 + README  ✅ —— examples/sentiment(dataset→分类器 batch→合并 code→End) 已真跑通
     (run succeeded，accuracy=0.8333，6 行逐条走 sidecar)；补齐前置缺口：
       · dataset 接入层(pre-mortem 抓出：有存储+执行器却无入口)：/v1/datasets CRUD + swf dataset-create/list/get
       · plan-apply 补 batch+params 字段(否则声明式建图开不起 batch、塞不进 code)
       · validator 理解 batch 语义(迭代输入 array→元素类型不报错；聚合输出 items/count 免声明)
       · 顶层 README(架构图+30分钟起步+容器/本地双路径+batch vs plan-apply 辨析+鉴权说明)
  #37 M10 全量验证  ✅ —— go build ✅ / go vet -tags integration ✅ / go test -race ./...(单元) ✅ /
     完整 integration 套件(-tags integration -race，MySQL+真 sidecar) 14 包全绿 ✅ / gofmt 一致 ✅ /
     5 Dockerfile + .dockerignore + compose 齐全(YAML 结构+接线断言过；docker 本机不可用故未 live 拉起)
  #31 独立 export  ✗ 已撤销（见反思⑤：导出=图内节点，对齐 Byteval）
  实证修复（真跑 integration/E2E 抓出，非设计文档能发现）：
    · 存储层系统性 bug：nullable JSON 列存 SQL NULL → json.RawMessage 无法 Scan → Get 崩。
      修 normalizeJSON 空输入返 "null" 字面量，一处修全 application(input/output_schema/config)+
      dataset(col_schema)；ErrDatasetRowsNotArray→400 INVALID_ROWS
    · M6_E2E 既有测试 bug：publish 把 version_lock+1，但后续 PUT 草稿硬编码锁 0 → 必冲突。
      修为「先 GET 读锁再回填」，不脆弱
    · 运维教训(E2E 真跑印证 #35 R3)：server 用 Redis 默认值走 asynq，若不起 worker 则 run 永远 pending
  遗留(诚实说明)：容器五件套未在本机 live 拉起(开发机无 Docker)；README 已标注，待有 Docker 的机器验证
反思决策（承重项，先对齐真实代码再落，纠正设计）：
  ① 纠正1（执行器需 DB 访问）：现有执行器全无状态，ExecContext 只有 Node/Inputs/
     Pool/RunID（node.go），拿不到 store；但 application 表已带 kind(http/python/rpc)+
     config(endpoint/headers/code)。决策：不新造远程调用层——ApplicationExecutor 按
     app_id 加载应用行，依 kind 委托给既有 HTTP/code 执行逻辑；store 访问经注册表 Config
     注入 AppResolver/DatasetResolver 接口（沿用 CodeExecutor{SidecarURL} 的 DI 范式，
     不碰全局单例）。顺带补齐 M8「application 节点可建不可执行」的缺口（scheduler.go:297
     的 "no executor registered"）
  ② 纠正2（扇出不改调度核心）：调度器是「每节点跑一次」的干净 DAG、node_run 与节点 1:1；
     真做 scheduler fan-out 会让节点状态按 item 爆炸、破坏 node_run 映射。而 dsl.Batch
     字段(SourceNode/ItemName/Size)已存在但只存不执行（render.go:156「MVP 无执行器」）。
     决策：batch = 包在任意执行器外的通用 map 包装——node.Batch.Enable 时解析源数组，
     逐 item 注入 ItemName 循环调底层执行器并聚合数组输出。调度器零改动，补上 Batch 执行缺口
  ③ 纠正3（dataset 不再是空壳）：KindDataset 已被 validator 要求 dataset_id
     (validator.go:131) 但无表/无查询/无执行器。决策：建 dataset 表(rows JSON)+CRUD+
     DatasetExecutor（按 dataset_id 加载行集，输出 rows[]），补齐 M8 甩到 M10 的存储与执行器
  ④ 纠正4（鉴权/限流不能破坏绿测试）：现仅 Recovery+日志中间件、config 无鉴权字段、
     e2e_integration 与所有 CLI httptest 依赖裸放行。决策：做成可选——config.Auth.APIKeys
     为空=放行（dev 与既有测试零改动）；配置后校验 X-API-Key；/healthz* 与 SSE 长连接
     豁免限流；限流用 golang.org/x/time/rate 令牌桶
  ⑤ 纠正5（撤销独立 export，事后反思 + Byteval 实证）：曾实现 swf export + /export 端点，
     让系统去翻节点/猜主表/拍 CSV。查 Byteval：导出是「工作流内的节点」（结果合并胶水节点
     + 写飞书表 application 节点，End 定义导出字段），无独立导出命令。且「输出什么」应由用户
     经 End 节点决定，非系统代劳。决策：撤销 export（删命令/端点/service），导出回归为用户可搭的
     图内节点；系统只保证「读完整 run 结果」（GET /runs/:id 已具备）。start/end 各仅一个节点
落地说明：
  存储：migration 00003 建 dataset 表(dataset_id/project_id/name/rows JSON/schema)；
    rows 整存 JSON 列（不建子表，适配千级 case 评测集）；
    sqlc 生成 CRUD + Search（复用 likePattern 转义）；DatasetService
  执行器：nodes.Config 扩 AppResolver/DatasetResolver 接口注入；
    ApplicationExecutor(Type "application")：按 app_id 取应用→kind=http 复用 HTTPExecutor、
    kind=python 复用 CodeExecutor、kind=rpc 暂返明确未支持错误；
    DatasetExecutor(Type "dataset")：按 dataset_id 取行集输出 rows[]；
    batchWrap：在 scheduler.execNode 内统一包装（取执行器后、Execute 前判 batch 开启），
    对所有节点类型生效、node_run 记聚合结果；对齐 Byteval ASL data.inputs.batch
    （batchEnable/batchSize/inputLists[]，迭代源为数组端口），逐 item 注入迭代变量循环调底层执行器
  结果落地（无独立 export，对齐 Byteval）：用户在图内接「结果合并」code 胶水节点 +
    「写表/上传」application 节点，End 定义输出字段；系统提供 GET /runs/:id 读完整结果，不代做导出
  复用/批量（对齐 Byteval builder 实证）：
    swf clone-ref --workflow-id（拉已发布 DSL→ToIR→本地会话，接已有 clone.go；
      蓝图术语 clone-ref，IR.Meta.Source 记来源）；
    swf plan-apply --sid --file plan.json（声明式建图：一份 JSON plan 批量落 session 草稿，
      对齐 Byteval「plan apply 按 JSON plan 批量创建 session 草稿」——nodes[] 内联 bindings[]、
      edges[] 可带 source_port/target_port，替代逐条 add-node/add-edge/bind）；
    附 swf plan-schema 打印各 kind 的 plan.json 节点规格（对齐 Byteval plan schema，Agent 自检字段）
  加固：middleware.go（APIKey 校验 + rate limit，可选）；config 加 Auth/RateLimit；
    router 挂中间件（healthz/SSE 豁免）；优雅退出已具备（server/worker）
  部署：cmd/{server,worker,swf} 与 sidecar 各写 Dockerfile；compose 补 server/worker/
    sidecar 服务(依赖 mysql/redis healthcheck)；Makefile 加 build-swf/镜像构建；
    README（架构图+30 分钟起步+样例一键脚本+故障排查）
  样例：examples/ 放 dataset 种子 + 建图脚本(swf 命令序列，分类器用 application 节点 kind=python
    →sidecar，真实演示 M8 能力发现+M10 执行器全链路；结果落地用图内「结果合并」code 节点 + End)
    + 一键 run --stream + GET /runs/:id 看结果
  测试：dataset CRUD、ApplicationExecutor(http/python 委托+rpc 未支持)、
    batchWrap(逐项+聚合+空数组)、middleware(空配置放行/校验/豁免)、
    clone-ref/plan-apply(声明式建图往返)；E2E integration 串 dataset→分类器(batch)→结果节点；-race + vet 全绿
范围界定（诚实说明）：
  - 结果导出：无独立 export 命令/端点（对齐 Byteval：导出是图内节点）；用户经 code/application
    节点 + End 自定义输出，系统只提供 GET /runs/:id 读完整结果
  - RPC 应用：ApplicationExecutor 对 kind=rpc 返回明确「未支持」错误（无 RPC 传输层），
    样例分类器用 kind=python(sidecar) 或 http；rpc 执行器留待有真实 RPC 后端时补
  - workflow 节点（子流程 KindWorkflow）：本阶段不实现嵌套子流程执行器，留到需要时
  - 限流粒度：进程内全局令牌桶（非按 key/租户配额），多实例部署需外置限流，随规模再说
  - plan-apply 定位：本阶段是「声明式建图」（JSON plan→session 草稿，对齐 Byteval），
    属构建期能力；不含「批量起 N 个 run」——单图内批量评测由节点级 batch 覆盖
  - 批量评测形态：本轮用「单图 + dataset 节点 + 分类器 batch」覆盖；Byteval 的完整最佳实践
    是「父工作流(batch 调度)+子工作流(batchSize=1 单条)」，嵌套子流执行器留到 KindWorkflow 落地时
```

---

## 3. 目录结构

```text
Smart-Workflow/
├── Smart-Workflow_Agent-CLI设计.md   # 设计蓝图
├── plan.md                            # 本文件
├── cmd/
│   ├── server/main.go                 # gin API
│   ├── worker/main.go                 # asynq 消费
│   └── swf/main.go                    # CLI
├── internal/
│   ├── dsl/                           # M-DSL: IR/DSL structs + 渲染器
│   ├── storage/mysql/                 # M-STORE
│   ├── validator/                     # M-VALID
│   ├── engine/
│   │   ├── varpool/                   # M-VARPOOL ★
│   │   ├── builder/                   # IR/DSL → Plan
│   │   ├── scheduler/                 # 依赖驱动 DAG 调度
│   │   └── nodes/                     # M-NODES 各执行器
│   ├── catalog/                       # M-CATALOG: search/app-schema/scope
│   ├── cache/                         # M-CACHE: Redis
│   ├── async/                         # M-ASYNC: asynq
│   ├── api/                           # M-API: gin handlers
│   └── cli/                           # M-CLI: cobra 命令 + session
├── sidecar/                           # M-SIDECAR: Python FastAPI
│   ├── main.py
│   └── runner.py
├── migrations/                        # goose SQL
├── deployments/docker-compose.yaml
├── configs/
└── test/
```

---

## 4. 关键技术决策记录

```text
1. DSL 字段对齐 PaiFlow（nodeMeta/inputs/schema/value/retryConfig）
   → 可与 PaiFlow 互导，验证设计血缘，便于对照学习
2. MySQL JSON 列存 DSL，不拆节点/边表
   → 加节点类型不改表结构（PaiFlow 已验证）
3. Python 用户代码走 sidecar HTTP，不在 Go 进程内跑
   → 隔离风险、职责单一；MVP 子进程隔离，二期升级容器沙箱
4. 执行依赖 = edges ∪ bindings（§10.6）
   → 数据引用自动补控制依赖，杜绝提前调度
5. IR 用可读 ID（app_1），DSL 用 type::uuid
   → Agent 友好 + 执行防冲突，idMap 桥接
6. 变量池在内存，大对象二期走 MinIO+url
   → MVP 只处理文本/小 JSON，文件类留二期
7. 画布是确认层不是编辑层
   → CLI 生成+校验，upload 草稿后画布看图（二期）
8. DB 访问用 sqlc，不用 GORM
   → 写 SQL 生成类型安全代码，JSON 列查询可控、无反射损耗，长期可维护
9. 引擎入口 engine.Run(ctx, runID) 从一开始就按异步设计
   → 状态/输入/输出全落库，不依赖调用方内存；
     同步 handler 与 asynq worker 复用同一入口，M9 加队列不重构
```

---

## 5.1 技术债清单（M5 反思沉淀）

> ①②③ 已在 M5 反思后修复（见 M5 小节）。以下为**已知、暂缓**项，
> 每条带触发条件与目标里程碑，避免"隐性欠债"。

```text
TD-1  Python 版本声明不一致 ✅ 已对齐（M6）
  现状：venv 实际 Python 3.9.6，原 plan/设计文档写 3.11
  影响：runner.py 目前只用标准库、无 3.10+ 语法，暂不炸；
        一旦用 match / X|Y 类型注解会在部署环境失败
  处理：已把技术栈表声明统一为 Python 3.9+（成本最低、不改运行时）；
        若后续需要 3.10+ 语法，再决定升 venv
  目标：M6（已完成）

TD-2  code 节点冷启动开销
  现状：每次执行都 subprocess 拉起新解释器（~50-100ms 冷启动）
  影响：M10 "评测集→分类器" 批量场景下成为吞吐瓶颈
  处理：重构为常驻 Python worker 池（预热解释器 + 任务分发）
  目标：M10（或批量场景性能压测暴露后）

TD-3  超时 kill 不覆盖子孙进程
  现状：subprocess.run(timeout=) 只杀主子进程
  影响：用户代码 fork / 起子进程时会留孤儿进程
  处理：start_new_session=True 起进程组，超时杀整个进程组
  目标：M10 加固

TD-4  裸 fd1 结果通道可被污染
  现状：runner 用 os.write(dup 的 fd1) 回传结果，只重定向了 sys.stdout
  影响：用户代码经 C 扩展直接写 fd 1 会污染结果 JSON，导致解析失败
  处理：结果改走独立 fd（如 fd 3 / 临时文件 / 命名管道），彻底隔离
  目标：M10 加固

TD-5  "隔离"非真沙箱（安全）
  现状：仅进程隔离 + 超时 + 输出大小限制；
        用户代码可 os.system、读写文件、发网络、读 sidecar 环境变量，
        且未限内存/CPU，一句 [0]*10**9 可 OOM 整个 sidecar
  影响：不可信代码执行有逃逸/越权/拒绝服务风险；文档不能称"沙箱"
  处理：容器化执行（gVisor/nsjail/seccomp + cgroups 限内存CPU + 只读FS + 无网络）
  目标：二期（上不可信多租户前必须做）

TD-6  RUNTIME_ERROR traceback 泄露服务器路径 ✅ 已修复（M6）
  现状：错误信息曾含 /Users/.../runner.py 完整栈，原样返回调用方
  影响：生产环境信息泄露
  处理：runner.py 已脱敏——对外 message 只保留异常类型+消息+<user_code> 帧，
        完整 traceback 写入 logs（服务端可见）；已加负向验证
  目标：M6（已完成）

TD-7  engine 真实装配链路未端到端验证 ✅ 已修复（M6）
  现状：cmd/server/main.go 曾未接 store/engine；
        code 节点只在测试里注入 URL 验证过
  影响："config→server→engine→code→sidecar" 全链路尚未真跑
  处理：main.go 已接 mysql.Open + engine.New(store, sidecarURL)；
        补 E2E integration 冒烟覆盖 create→validate→run→node-debug
  目标：M6（已完成）

TD-8  后台 run goroutine 三重缺陷 ✅ 已修复（M6 反思）
  现状（曾）：createRun 内裸 go func 调 engine.Run，且用 context.Background()——
        (1) 无 panic 兜底：gin.Recovery 护不到自起 goroutine，run panic 直接崩进程；
        (2) 逃逸优雅退出：srv.Shutdown 不等在途 run，重启丢任务、留僵尸 running；
        (3) 无并发上限：每请求一条 goroutine + sidecar 连接，批量易 OOM/打爆 sidecar
  处理：新增 api.RunDispatcher——semaphore 限并发（默认 16）+ 每 goroutine recover
        落 failed（engine.FailRunWithError 打墓碑）+ 可取消 baseCtx + WaitGroup，
        main.go 优雅退出时 dispatcher.Shutdown(ctx) drain 在途 run；
        满载/关停时 Submit 返 false，createRun 改判 failed 并回 503 RUN_REJECTED
  说明：这是「M9 换 asynq 前」的进程内兜底，HTTP 契约不变；
        M9 只需把 Submit 换入队、Shutdown 换停消费者
  目标：M6（已完成）

TD-9  run version=0 语义重载 ✅ 已修复（M6 反思）
  现状（曾）：CreateRun 的 version int32 把 0/缺省/-1/正数塞四义，
        JSON 缺省零值=0，"忘传 version" 被静默当成"跑已发布"，Agent 拿误导结果
  处理：createRunReq.Version 改 *int32；resolveVersion 用指针消歧——
        nil(省略)=最新发布 / -1=草稿 / N>0=指定 / 显式 0 或 < -1 → ErrInvalidVersion
        （400 INVALID_VERSION），不再静默兜底
  目标：M6（已完成）

TD-10 run 前无 validation gate ✅ 已修复（M7）
  现状（曾）：POST /runs 不调 validate，坏图（缺 start/环/坏 ref）一路跑进 builder/scheduler，
        落 failed 但错误是裸字符串，不是 validate 的结构化 issues
  影响：Agent/CLI 期望 run 失败能拿到可定位的 issue 列表；PaiFlow 心智是"validate 过才 run"
  处理：RunService.CreateRun 在 resolveVersion 后、落 pending 前插 validateForRun gate：
        加载目标版本（草稿/已发布）DSL → validateDSL，HasError 则返回 *ValidationError
        （携全部 issues），不落库、不进 builder；helpers.failFromErr 映射为
        422 VALIDATION_FAILED + details.issues。CLI run 命令原样透传，Agent 据此修复
  目标：M7（已完成）

TD-11 API handler 与具体 service 强耦合
  现状：handler 持 *service.XxxService 具体类型而非接口，
        除 helpers 与 E2E 外，handler 业务分支无法脱库单测
  影响：一旦给 handler 加鉴权/限流/参数转换（M10），可测面变窄
  处理：抽 service 接口，handler 依赖接口，支持 mock 单测
  目标：M10 加固（或 handler 逻辑变重时）

TD-12 列表/大对象 API 契约债（趁未被 CLI 锁死前定）
  现状：(1) listRuns/listWorkflows/listApplications 只回 {items:[]}，无 total/cursor，
            客户端只能靠 len==limit 猜下一页；
        (2) GetRun 一次性全量返回所有 node_run（ListNodeRuns 无 limit）+ 每个 node
            的 input/output 全 JSON，大图/大 output 时响应体膨胀；
        (3) node-debug assertions 硬编码 3 条，Agent 无法自定义断言表达式
  影响：契约一旦被 M7 CLI 锁定，再改分页/裁剪/断言 DSL 都是 breaking change
  处理：列表加 total 或 cursor；GetRun 的 node_run 分页 + 大字段可选裁剪；
        assertions 升级为可配置表达式（真 eval 卖点）
  目标：M8（能力发现/契约定型期）；断言 DSL 可延至 eval 场景

```

---

## 5. 风险与应对

```text
风险1  变量池路径解析边界多（数组越界/类型不符）
  → 直接翻译 PaiFlow VariablePoolTest 用例做测试基线，先测后写
风险2  Go/Python sidecar 联调耗时（协议、超时、错误透传）
  → M0 就把 sidecar /healthz 打通，M5 只加 /run，不留到最后
风险3  asynq/SSE/Redis 三件套拉长工期
  → 归到 M9，前 8 个里程碑不依赖它们，闭环先跑通
风险4  校验器检查项多容易漏
  → 以设计文档 §11.2 清单为准，每条一个测试用例
风险5  节点类型膨胀
  → MVP 只做 start/end/condition/http/code/llm 六种，其余二期
```

---

## 6. 优先级铁律

```text
先做（心脏）：M-DSL / M-VARPOOL / M-VALID / M-ENGINE
  这四个定义了"工作流是什么、怎么校验、怎么跑"，松散则后面难补
再做（接缝）：M-SIDECAR / M-API / M-CLI
  打通 Go↔Python、对外接口、Agent 入口
最后（增强）：M-CACHE / M-ASYNC / M-CATALOG / 画布
  性能与体验优化，不影响核心闭环
```

> 一句话：先把 `IR→DSL→validate→变量池→执行` 的核心链路写扎实，再接 sidecar 和 CLI，最后补缓存/异步/能力发现。
