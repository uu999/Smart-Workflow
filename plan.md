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

### M8 · 能力发现（M-CATALOG）
```text
交付：
  swf search（app/workflow/dataset）
  swf app-schema（拉 schema 缓存到 app_cache）
  swf scope（按连通性+类型过滤可绑定候选）
验收：
  Agent 能先 search 再 app-schema 再 scope，把"猜端口"变"选端口"
```

### M9-a · Redis 缓存 + 异步执行（M-CACHE + M-ASYNC）
```text
交付：
  Redis：定义缓存 swf:def / 引擎缓存 swf:plan / 运行态 swf:run / 运行锁
  asynq：长工作流入队，swf-worker 消费；run 立即返回 runID
    —— 复用 M4 的 engine.Run(ctx, runID)，worker 只是它的调用方之一
  失败重试 + 并发闸门（worker 并发度可配）+ 优先级队列（调试优先/批量靠后）
  GET /runs/{id} 轮询状态（pending/running/succeeded/failed）
验收：
  长任务异步跑不卡请求；服务重启后 worker 能捡回在途任务；
  引擎缓存命中省重复构建；轮询能拿到运行状态
```

### M9-b · SSE 流式推送（M-CACHE 事件流）
```text
交付：
  运行事件写 Redis Stream：swf:run:{id}:events
  SSE 接口：GET /runs/{id}/events，推 node start/process/end 事件
  swf run --stream：CLI 订阅并打印每步（对标 PaiFlow /api/workflow/chat）
验收：
  run --stream 实时看到每个节点的状态流转与输出，无需轮询
```

### M10 · 端到端样例 + 加固
```text
交付：
  E2E 样例：评测集→分类器→结果导出
  clone-ref + plan apply（复用与批量）
  鉴权 API Key + 限流 + 优雅退出
  部署文档 + README
验收：
  新机器按文档 30 分钟起；样例一键跑通；失败有可读错误
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
