# M6 · HTTP API + node-debug 实施计划

## Context（为什么做这件事）

前 5 个里程碑已把「IR→DSL→validate→变量池→执行引擎（含 Python sidecar code 节点）」的核心链路写扎实，但这些能力目前只能通过 Go 测试触达——**没有对外接口**。M6 要把它们暴露成 HTTP API，让 M7 的 `swf` CLI（Agent 入口）有地基可站。

M6 的核心验收是一条可 `curl` 跑通的链路：`create workflow → validate → run → node-debug`。同时借装配 server 的机会，把 M5 反思沉淀的 3 条技术债一并清掉（都挂在 M6）：
- **TD-1**：Python 版本声明 3.11 vs venv 实际 3.9.6 不一致
- **TD-6**：`RUNTIME_ERROR` traceback 泄露服务器绝对路径，对外需脱敏
- **TD-7**：`cmd/server/main.go` 从未接 store/engine，全链路 config→server→engine→code→sidecar 未真跑

**已确认的两个关键决策**（来自用户）：
1. **run 执行模型**：`POST /v1/runs` 先落 pending 记录 → 后台 goroutine 调 `engine.Run` → 立即返回 `runID`；`GET /v1/runs/{id}` 轮询状态。与设计文档/M9 异步契约一致，M9 只需把 goroutine 换成 asynq 入队，不改 HTTP 契约。
2. **资源 API 范围**：project / workflow / application / run **四类全 CRUD**。

---

## 现状与可复用资产

- **HTTP 骨架**：[router.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/router.go) 已有 `NewRouter(Deps)`、`/healthz`、`/healthz/sidecar`、`/v1/ping`，用 [httpx.OK/Fail](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/httpx/response.go) 统一 envelope。
- **service 层**：[WorkflowService](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/workflow.go) 已有 Create/Get/UpdateDraft（乐观锁）/Publish/GetVersion，含 `genID`/`marshalDSL`/`unmarshalDSL`/`toInt32` helper 和 `ErrNotFound`/`ErrVersionLock`。
- **引擎**：[engine.New(store, sidecarURL)](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/run.go)、`Engine.Run(ctx, runID)` 已是纯函数入口；节点执行器走注入的 [nodes.Registry](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/nodes/node.go)。
- **校验器**：[validator.Validate(ir)](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/validator/validator.go) 接收 IR；[dsl.ToIR(dsl, meta)](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/dsl/clone.go) 可把存储的 DSL 反渲染成 IR 供校验。
- **sqlc**：project/workflow/application/run 查询大部分已生成（见 [querier.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/storage/mysql/gen/querier.go)）。缺口仅 **project 的 Update / SoftDelete**（application 已全，workflow 已全，run 无需改）。

---

## 实施步骤

### 步骤 1 — 补齐 sqlc 查询（project 全 CRUD 缺口）
- 在 [project.sql](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/storage/mysql/queries/project.sql) 增 `UpdateProject`（name/description）、`SoftDeleteProject`（set deleted_at）。
- `sqlc generate` 重新生成（绝对路径 `~/go/bin/sqlc`，`gofmt` 生成物）。
- application/workflow/run 查询已够用，不改。

### 步骤 2 — 扩展 service 层（新增 3 个 service，复用 workflow.go 的模式）
新增文件，均复用 `genID`/`toInt32`/错误哨兵，构造函数 `NewXxxService(store)`：
- `internal/service/project.go`：Create/Get/List/Update/Delete。
- `internal/service/application.go`：Create/Get/List/Update/Delete；create/update 校验 input_schema/output_schema/config 为合法 JSON（`json.Valid`）。
- `internal/service/run.go`：`CreateRun(workflowID, version, input, trigger)` 落 pending 记录返回 runID；`GetRun(runID)`（含 node_run 列表）；`ListRuns(workflowID, page)`。
  - 版本解析：version 传 0/缺省→取 workflow.published_ver；显式 -1→草稿调试（对齐 `engine.DraftVersion`）。
- workflow：给 [WorkflowService](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/workflow.go) 补 `List` 和 `Delete`（复用已有 sqlc `ListWorkflows`/`SoftDeleteWorkflow`）。

### 步骤 3 — validate service（DSL→IR→Validate）
- 新增 `internal/service/validate.go`：给定 workflowID（草稿或指定版本），读 DSL → `dsl.ToIR` → `validator.Validate` → 返回 `*validator.Result`。
- 供 `POST /v1/workflows/{id}/validate` 使用。

### 步骤 4 — node-debug service（评测信号核心，抄 PaiFlow §4）
- 新增 `internal/service/nodedebug.go`：
  - 入参：workflowID + nodeID + 调用方提供的 `inputs`（调试直接喂输入，**不做变量池上游解析**）。
  - 取 workflow DSL 找到该 DSLNode，**强制 `RetryConfig.ShouldRetry=false`**（拿即时真实结果，抄 PaiFlow `node_debug`）。
  - 用引擎持有的同一 registry 取执行器，构造 `ExecContext{Node, Inputs, Pool: varpool.New(), RunID: "debug"}`，`Execute` 计时。
  - 返回结构对齐设计文档 §4：`node_id/node_type/status/input/output/exec_cost_sec/assertions`（raw_output/token_cost 当前节点类型无则省略）。
  - **assertions**（新增，MVP 3 条）：`output_not_empty`、`status_success`、`cost_under_sec`（阈值可选参数）。
  - 为让 debug service 复用引擎 registry：给 [Engine](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/run.go) 加一个只读导出方法 `DebugNode(ctx, node, inputs, costTarget)`，把单节点执行 + 关重试 + assertions 收在 engine 包内（registry 是 engine 私有字段，debug 逻辑放这里最自然，service 只做编排）。

### 步骤 5 — API handlers + 路由（gin）
- 新增 `internal/api/handlers.go`（或按资源拆 `projects.go`/`workflows.go`/`applications.go`/`runs.go`）。
- 扩展 [Deps](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/router.go)：加 `Store *mysql.Store`、`Engine *engine.Engine` 及各 service（或在 NewRouter 内用 store 构造 service）。
- 路由（全部 `/v1` 下，统一 httpx envelope）：
  - `projects`：POST / GET/{id} / GET(list) / PUT/{id} / DELETE/{id}
  - `applications`：同上 5 个
  - `workflows`：POST / GET/{id} / GET(list) / PUT/{id}（草稿+乐观锁，body 带 version_lock）/ DELETE/{id} / POST/{id}/publish / POST/{id}/validate
  - `runs`：POST（建 run + 后台 goroutine 跑）/ GET/{id}（状态+node_runs）/ GET(list by workflow) / POST /workflows/{id}/node-debug
- 错误映射 helper：`ErrNotFound→404 NOT_FOUND`、`ErrVersionLock→409 VERSION_CONFLICT`、校验/参数错→400、其余→500，全部走 `httpx.Fail`。
- run 端点：调 `runSvc.CreateRun` 拿 runID 后，`go func(){ engine.Run(context.Background(), runID) }()`（用独立 ctx，不随请求取消），立即 `httpx.OK{run_id, status:"pending"}`。

### 步骤 6 — 装配 server（清 TD-7）
- 改 [cmd/server/main.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/cmd/server/main.go)：`mysql.Open(cfg.MySQL.DSN)` → `engine.New(store, cfg.Sidecar.BaseURL)` → 各 service → `api.NewRouter(Deps{...})`；`defer store.Close()`。DB 连不上时 fatal 并给出可读日志。

### 步骤 7 — 清 TD-6（traceback 脱敏）
- [sidecar/runner.py](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/sidecar/runner.py) 的 `RUNTIME_ERROR`：对外 message 只保留异常类型+消息+**用户代码帧**（`<user_code>`），裁掉含 runner.py 绝对路径的框架帧；完整 traceback 仍留在 logs（服务端可见）。

### 步骤 8 — 清 TD-1（Python 版本对齐）
- venv 实际 3.9.6。把 [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) 技术栈表与 TD-1 的声明统一为 **Python 3.9+**（runner 仅用标准库、无 3.10+ 语法，成本最低且不改运行时）；在 TD-1 标注「已对齐声明」。

### 步骤 9 — 测试
- **service 单测**（无 DB 的纯逻辑：version 解析、assertions 判定、错误映射）。
- **API handler 测试**：用 `httptest` + gin，mock service 或用真实 store（integration tag）。核心是错误码映射与 envelope 结构。
- **E2E integration 测试（清 TD-7 验收）**：`-tags integration`，起真实 MySQL + mock HTTP sidecar，跑 `POST /workflows → POST /validate → POST /runs → 轮询 GET /runs/{id} 到 succeeded → POST /node-debug`，断言 envelope 与 node_run 落库。
- Go `RUNTIME_ERROR` 脱敏后契约不变（golden fixture 只锚字段名，不锚 message 内容），[contract_test.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/nodes/contract_test.go) 应仍绿；补一个 Python 侧断言：message 不含绝对路径。

---

## 验证方式

```bash
# 1. 编译 + vet + race 全绿
go build ./... && go vet ./... && go test -race ./...

# 2. 契约守卫（脱敏后仍对齐）
go test ./internal/engine/nodes/ -run TestContract
python3 sidecar/contract/test_contract.py

# 3. 起 sidecar + server，真实 curl 全链路（清 TD-7）
sidecar/.venv/bin/python -m uvicorn main:app --port 8090 --app-dir sidecar &
go run ./cmd/server &
curl -s -XPOST localhost:8080/v1/workflows -d '{...}'      # 建 workflow
curl -s -XPOST localhost:8080/v1/workflows/{id}/validate   # 0 error
curl -s -XPOST localhost:8080/v1/runs -d '{"workflow_id":"...","input":{...}}'
curl -s localhost:8080/v1/runs/{run_id}                    # 轮询到 succeeded
curl -s -XPOST localhost:8080/v1/workflows/{id}/node-debug -d '{"node_id":"...","inputs":{...}}'

# 4. E2E integration（真实 MySQL）
go test -tags integration ./internal/api ./internal/service ./internal/engine
```

**验收对齐 plan.md M6**：`curl 完成 create→validate→run→node-debug 全链路`；顺带 TD-1/6/7 三债清除，并在 plan.md 标注 M6 完成。
