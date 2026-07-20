# M6 收尾 · 测试补齐与全链路验证（step 9）

> 配套：[M6 实施计划](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/.trae/documents/M6_HTTP_API_and_node_debug.md) · [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md)
> 定位：**M6 的代码主体（步骤 1–8）已落地**，本计划只做 **步骤 9（测试 + plan 标注 + 全链路验证）**，不重复已完成的实现。

---

## Summary

M6（HTTP API + node-debug）的实现主体已在上一轮完成并可编译通过。本次「进入 M6」实为**接续被上下文丢失打断的步骤 9**：补齐尚缺的测试、清 TD-7 的 E2E 验收、补 TD-6 的 Python 侧脱敏断言，最后在 [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) 标注 M6 完成并跑通全量验证。

范围（新增 3 个测试文件 + 1 处 plan 标注 + 验证命令）：
1. `internal/api` handler 层测试（非集成，纯逻辑：错误码映射 + envelope + 分页）。
2. `internal/api` E2E 集成测试（`integration` tag，真实 MySQL + mock HTTP sidecar，走 create→validate→publish→run→轮询→node-debug 全链路，断言 node_run 落库）。
3. `sidecar` runner 脱敏断言（TD-6 验收：RUNTIME_ERROR message 不含服务器绝对路径，完整 traceback 仍在 logs）。
4. [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) M6 小节标注 ✅ + 落地说明。
5. 全量验证：build / vet / `-race` 单测 + 集成测试 + 契约守卫。

---

## Current State Analysis（Phase 1 探查结论）

**已在磁盘、已编译通过（无需改动）：**
- 步骤 1 sqlc：[project.sql.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/storage/mysql/gen/project.sql.go) 已含 `UpdateProject`/`SoftDeleteProject`（[project.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/project.go) 已引用）。
- 步骤 2 service：[project.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/project.go)、[application.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/application.go)、[run.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/run.go)、[workflow.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/workflow.go)（含 List/Delete）全部到位。
- 步骤 3/4：[validate.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/validate.go)、[nodedebug.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/nodedebug.go) + [engine/debug.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/debug.go)（DebugNode + 3 断言）到位。
- 步骤 5 API：[router.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/router.go)（4 类资源全路由，`d.Store != nil` 才挂载）、[helpers.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/helpers.go)（`failFromErr`/`failBadRequest`/`pageParams`）、[projects.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/projects.go)/[applications.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/applications.go)/[workflows.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/workflows.go)/[runs.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/runs.go)（异步 goroutine run 模型）到位。
- 步骤 6 装配：[cmd/server/main.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/cmd/server/main.go) 已 `mysql.Open` → `engine.New(store, cfg.Sidecar.BaseURL)` → `api.NewRouter`。
- 步骤 7 TD-6：[runner.py](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/sidecar/runner.py) `_sanitize_error` 已实现。
- 步骤 8 TD-1：[plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) 技术栈表 + TD-1 已统一为 Python 3.9+。
- 步骤 9 **部分**：[engine/debug_test.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/debug_test.go)（3 个 DebugNode 用例）、[service/application_test.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/application_test.go)（normalizeJSON）、[service/validate_test.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/validate_test.go)（2 个 validateDSL）已通过。

**验证过的事实：**
- `go build ./...` / `go vet ./...` 干净；`go test ./...` 全绿（cached）。
- `swf-mysql`（:3308）/ `swf-redis`（:6381）容器 healthy；`smart_workflow` 库 6 张业务表 + goose 版本齐全 → **集成测试本机可跑**。
- `internal/api` 目前 **零测试文件**；`internal/engine` 有 M4 `run_integration_test.go`、`internal/service` 有 M2 `workflow_integration_test.go`（两者都用 `//go:build integration` + `SWF_TEST_DSN` 兜底 `swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true`，含 `testStore(t)` helper）。

**缺口（本计划要补的）：**
- `internal/api` 无 handler 层测试（错误码映射 / envelope 结构未被守卫）。
- 无 E2E 集成测试串起 API→engine→sidecar（TD-7 验收缺口）。
- TD-6 只有 Go 侧「字段结构」契约（[contract_test.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/nodes/contract_test.go) / [test_contract.py](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/sidecar/contract/test_contract.py)），无「message 不含绝对路径」的行为断言。
- [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) M6 小节未标 ✅。

---

## Proposed Changes

### 变更 1 — 新增 `internal/api/helpers_test.go`（非集成，纯逻辑）

**Why**：`failFromErr` 的错误码映射与 `pageParams` 的边界是对外契约的地基，且可零 DB 覆盖。这类测试能进 `go test -race ./...` 常态门禁。

**How**：用 `gin.CreateTestContext(httptest.NewRecorder())` 构造 `*gin.Context`，直接调 helper，再对 recorder 反序列化 `httpx.Envelope` 断言。
- `TestFailFromErr_Mapping`：表驱动，覆盖四类 + 兜底：
  - `service.ErrNotFound` → 404 / `code=NOT_FOUND` / `ok=false`
  - `service.ErrNodeNotFound` → 404 / `NOT_FOUND`
  - `service.ErrVersionLock` → 409 / `VERSION_CONFLICT`
  - `service.ErrInvalidJSON` → 400 / `INVALID_JSON`
  - `errors.New("boom")`（兜底）→ 500 / `INTERNAL`
  - 每条断言 `Envelope.OK==false` 且 `Envelope.Error.Code` 匹配。
- `TestPageParams`：表驱动，覆盖默认（`limit=20,offset=0`）、自定义、上限截断（`limit=1000→200`）、非法值忽略（`limit=abc`/`offset=-5` 回落默认）。用 `c.Request = httptest.NewRequest("GET", "/x?limit=..&offset=..", nil)` 注入 query。

**引用**：断言目标类型来自 [httpx.Envelope/Error](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/httpx/response.go)；错误哨兵来自 [service 包](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/service/workflow.go)。

### 变更 2 — 新增 `internal/api/e2e_integration_test.go`（`//go:build integration`）

**Why**：清 TD-7 的验收——`config→server→engine→code→sidecar` 全链路真跑一遍；同时守卫 envelope 与 node_run 落库。这是 M6 plan 验收「curl 完成 create→validate→run→node-debug 全链路」的自动化版。

**How**：
- **mock sidecar**：`httptest.NewServer`，仅处理 `POST /run/python-code`，回 `{"ok":true,"data":{"outputs":{"answer":"hello"},"logs":""}}`（对齐 [code.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/engine/nodes/code.go) 的 `codeRunResponse`：`data.outputs`/`data.logs`）。
- **装配**：复用集成测试约定的 `testStore(t)`（读 `SWF_TEST_DSN`，兜底 :3308 DSN）；`eng := engine.New(store, mockSidecar.URL)`；`router := api.NewRouter(api.Deps{Cfg: &config.Config{Env:"dev", Sidecar: config.SidecarConfig{BaseURL: mockSidecar.URL}}, Logger: zap.NewNop(), Store: store, Engine: eng})`。
  - 注意：`NewRouter` 里 `d.Cfg.Env != "dev"` 会切 ReleaseMode，测试传 `Env:"dev"` 保持一致、避免污染全局 gin mode。
- **请求驱动**：辅助函数 `do(method, path, body) (*httptest.ResponseRecorder)`，内部 `httptest.NewRequest` + `router.ServeHTTP(rec, req)`，解析 `httpx.Envelope`。
- **流程**（每步断言 `ok=true` + 关键字段）：
  1. `POST /v1/projects {name}` → 取 `project_id`。
  2. `POST /v1/workflows {project_id, name, draft}`，draft = **start→code→end** 图：
     - `start::1`（KindStart，输出 `query:string`）
     - `code::1`（NodeType `code`，`nodeParam.code="sink({'answer': inputs['query']})"`，input `query` ref 自 `start::1.query`）
     - `end::1`（KindEnd，input `answer` ref 自 `code::1.answer`）
     - 三条 `RetryConfig: dsl.DefaultRetryConfig()`；边 start→code→end。
     - → 取 `workflow_id`。
  3. `POST /v1/workflows/{id}/validate` → 断言 `data.has_error == false`。
  4. `POST /v1/workflows/{id}/publish {change_log}` → 断言 `data.version == 1`。
  5. `POST /v1/runs {workflow_id, version:0, input:{"query":"hello"}}` → 断言 `data.status=="pending"`，取 `run_id`。
  6. **轮询** `GET /v1/runs/{run_id}`，最多 ~10s（`for` + `time.Sleep(200ms)`），直到 `data.status` 为终态；断言 `succeeded`，且 `data.nodes` 非空、各 node `status=succeeded`（验证 node_run 落库）。
  7. `POST /v1/workflows/{id}/node-debug {node_id:"code::1", inputs:{"query":"hi"}, cost_target_sec:5}` → 断言 `data.status=="succeeded"`、`data.assertions` 含 3 条且 `status_success` 通过。
  8. **错误码守卫**：`GET /v1/workflows/does-not-exist` → HTTP 404 且 `error.code=="NOT_FOUND"`；`POST /v1/workflows {}`（缺 project_id/name）→ 400 `BAD_REQUEST`。
- **清理**：`defer` 里按 `run_id`/`workflow_id`/`project_id` `DELETE`（含 `node_run`/`workflow_run`/`workflow_version`/`workflow`/`project`），沿用 M4 集成测试的直接 `st.DB.ExecContext` 清理法。
- **确定性**：run 是后台 goroutine（[runs.go](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/internal/api/runs.go)），故必须轮询到终态再断言/清理；`persistFinal` 最后才写 `succeeded`，轮询到 succeeded 即保证节点快照已落库，清理安全。

**注**：集成测试带 `integration` tag，不进 `-race ./...`（该命令不加 tag、不编译这些文件），故后台 goroutine 与轮询不构成 race 门禁问题。

### 变更 3 — 新增 `sidecar/test_runner_sanitize.py`（TD-6 行为断言，纯标准库）

**Why**：现有契约测试只锁「字段名」，不锁「message 内容」。TD-6 的核心承诺是「对外 message 不泄露服务器绝对路径、完整 traceback 只在 logs」，需一条行为断言守住，防回归。

**How**：仿 [test_contract.py](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/sidecar/contract/test_contract.py) 用 `subprocess.run([sys.executable, RUNNER], input=...)` 跑 [runner.py](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/sidecar/runner.py)：
- 用例 A：`code="raise ValueError('boom')"` → 断言 `ok==false`、`error.code=="RUNTIME_ERROR"`、`error.message` **不含** `"runner.py"` 且 **不含** sidecar 目录绝对路径前缀（用 `os.path.dirname` 计算），但 **含** `"ValueError"`。
- 用例 B：多帧（用户代码内嵌套函数抛异常）→ 断言 `error.message` 仅出现 `<user_code>` 帧、不含框架绝对路径；`logs` **含** 完整 traceback（即含 `Traceback` 且含 `runner.py`，证明完整栈留在服务端）。
- 退出码：全过 `return 0` 并打印 `sanitize OK`，否则打印失败项 `return 1`（与 test_contract.py 风格一致，`python3` 直接可跑）。

### 变更 4 —（可选，低成本）新增 `internal/service/run_test.go`（version 语义纯逻辑）

**Why**：`resolveVersion` 的 `-1`（草稿）分支在触库前 early-return，可零 DB 覆盖版本语义这一文档化行为。

**How**：`(&RunService{}).resolveVersion(context.Background(), "", service.DraftVersion)` → 断言返回 `-1, nil`；`>0` 分支同理返回原值。`0/缺省` 分支触库，留给集成测试覆盖（不在此测）。若发现该方法私有导致跨包不便，则与 run 同包 `package service` 内联测试即可。

### 变更 5 — 标注 [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) M6 完成

**How**：
- M6 标题改为 `### M6 · HTTP API + node-debug（M-API）✅ 已完成`。
- 在 M6 代码块尾部补「落地说明」（仿 M5 风格）：4 类资源全 CRUD 路由、异步 goroutine run 模型（提交即返回 runID + 轮询）、node-debug 关重试 + 3 断言、TD-1/6/7 已清、E2E 集成冒烟串起 create→validate→publish→run→node-debug。
- TD-1/6/7 已是 ✅（无需改）；确认措辞与「已完成」一致。

---

## Assumptions & Decisions

- **不重写已完成实现**：步骤 1–8 已编译通过且逻辑自洽，本计划只补测试与标注；如测试暴露 bug 再最小改动修复（不主动重构）。
- **API 测试分两层**：纯逻辑（helpers）进常态门禁；全链路（E2E）进 `integration` tag。原因：handler 持有具体 `*service.XxxService`（非接口），无接缝 mock，硬造 mock 需改生产代码，得不偿失；真实 store 本机可用，E2E 更有验收价值。
- **mock sidecar 而非真 Python 进程**：E2E 聚焦「Go 侧全链路 + 落库」，code 节点只需契约正确的 HTTP 响应；真 sidecar 冷启动/依赖已由 M5 integration + 契约守卫覆盖。
- **DSN/基础设施**：沿用 `SWF_TEST_DSN` 兜底 `swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true`；已确认容器 healthy、表齐全。
- **run 用 `version:0`（已发布）路径**：E2E 先 publish 再以 version=0 跑，覆盖 `resolveVersion` 的「取 published_ver」主路径；草稿路径由变更 4 单测覆盖。
- **计划文件位置**：置于项目内 `Smart-Workflow/.trae/documents/`，与既有 M6 实施计划同域，便于检索。

---

## Verification

```bash
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
cd /Users/bytedance/Downloads/Byteval/Smart-Workflow

# 1) 编译 + vet + race 单测全绿（不含 integration tag）
go build ./... && go vet ./... && go test -race ./...

# 2) 契约守卫（结构 + 脱敏行为）
go test ./internal/engine/nodes/ -run TestContract
sidecar/.venv/bin/python sidecar/contract/test_contract.py
sidecar/.venv/bin/python sidecar/test_runner_sanitize.py   # 新增：TD-6 行为断言

# 3) E2E 集成（真实 MySQL :3308 + mock sidecar，已确认容器 healthy）
go test -tags integration ./internal/api ./internal/service ./internal/engine
```

**通过标准：**
- `go build`/`vet` 无输出；`-race ./...` 全 ok（新增 helpers_test 覆盖错误映射/分页）。
- `TestContract`（Go）与 `test_contract.py`（Py）绿；`test_runner_sanitize.py` 打印 `sanitize OK`（message 不含绝对路径、logs 含完整栈）。
- `-tags integration` 下 `internal/api` E2E 走通 create→validate→publish→run→轮询 succeeded→node-debug，且断言 node_run 落库、错误码 404/400 映射正确；`internal/service`（M2）、`internal/engine`（M4）既有集成测试不回归。
- [plan.md](file:///Users/bytedance/Downloads/Byteval/Smart-Workflow/plan.md) M6 标 ✅ 并含落地说明。

---

## 交付物清单

| # | 文件 | 类型 | 状态 |
|---|---|---|---|
| 1 | `internal/api/helpers_test.go` | 新增（单测） | 待建 |
| 2 | `internal/api/e2e_integration_test.go` | 新增（`integration`） | 待建 |
| 3 | `sidecar/test_runner_sanitize.py` | 新增（Py 断言） | 待建 |
| 4 | `internal/service/run_test.go` | 新增（可选单测） | 待建 |
| 5 | `plan.md` | 标注 M6 ✅ | 待改 |
