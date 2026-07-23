# Smart-Workflow

面向 AI Agent 的工作流引擎：把自然语言需求增量构建成**可复用、可验证、可运行**的工作流。
Go 引擎 + Python sidecar（隔离执行用户代码）+ `swf` CLI（输出统一 JSON envelope，供 Agent 直接解析）。

## 架构

```text
                    ┌──────────────┐
   swf CLI ───────▶ │  swf-server  │ ──── MySQL (工作流/应用/评测集/运行)
  (建图/校验/运行)   │   (gin API)  │ ──── Redis (缓存 + asynq 队列 + SSE 事件流)
                    └──────┬───────┘
                           │ 入队 run
                    ┌──────▼───────┐
                    │  swf-worker  │ ──── engine.Run（DAG 调度 + 节点执行器）
                    │ (asynq 消费) │           │
                    └──────────────┘           │ code / application(python) 节点
                                        ┌───────▼────────┐
                                        │ Python sidecar │ 子进程隔离执行用户代码
                                        └────────────────┘
```

- **IR → DSL 两层模型**：IR 可增量编辑（add-node/bind/plan-apply），渲染成可执行 DSL。
- **执行后端可切换**：配 Redis 走 asynq（server 入队、worker 执行、SSE 从 Redis Stream 订阅）；
  无 Redis 则进程内 dispatcher 兜底 + 内存事件总线。切换不改 handler。
- **节点执行器**：start/end、code（Python sidecar）、application（按 kind 委托 http/python）、
  dataset（评测集行集）、condition（分支）；任意节点可开 **batch** 对数组输入逐条执行。

## 30 分钟起步

### 方式 A：容器一键起五件套（推荐用于试跑）

> ⚠️ **诚实说明**：本仓库的容器编排（compose + 5 个 Dockerfile）已按最佳实践写好并做过
> YAML/构建阶段静态校验，但**尚未在装有 Docker 的机器上做过 live 端到端拉起**（开发机无 Docker）。
> 若你的机器有 Docker，下面命令即为设计入口；如遇问题请对照「方式 B」逐件排查。

```bash
# 起 mysql + redis + sidecar + server + worker，migrate 服务自动建表
make up-all              # = docker compose -f deployments/docker-compose.yaml up -d --build

# 看日志 / 停
make logs-all
make down-all
```

容器内地址由 compose 经 `SWF_MYSQL_DSN`/`SWF_REDIS_ADDR`/`SWF_SIDECAR_BASEURL` 注入
（服务名 `mysql:3306`/`redis:6379`/`sidecar:8090`）；宿主机仍可用映射端口 `3308`/`6381`/`8090` 连基建调试。

### 方式 B：本地进程起（可逐件验证）

```bash
# 1) 起基建（仅 mysql + redis 容器）
make infra-up

# 2) 建表
make migrate-up

# 3) 起 sidecar（新终端）
make sidecar-install     # 首次：建 venv 装依赖
make run-sidecar         # 监听 127.0.0.1:8090

# 4) 起 server（新终端）
make run-server          # 监听 :8080；无 Redis 配置时用进程内 dispatcher，单进程即可跑通

# （可选）配了 Redis 时另起 worker：make run-worker
```

### 跑通端到端样例（情感评测）

见 [examples/sentiment](examples/sentiment)：评测集 → 分类器（逐条 batch）→ 结果合并 → End。

```bash
export SWF_SERVER_URL=http://127.0.0.1:8080
# 若未把 swf 装进 PATH，可： export SWF="go run ./cmd/swf"

bash examples/sentiment/01_seed.sh   # 建 project + dataset + 分类器 application
bash examples/sentiment/02_run.sh    # plan-apply 建图 → validate → upload → run --stream
```

预期：`run --stream` 实时打印各节点事件，最终结果含 `accuracy`/`total`/`details`（准召统计）。

## swf CLI 速览

| 命令 | 作用 |
|---|---|
| `swf search --kind application\|workflow` | 能力发现：搜可复用应用/工作流 |
| `swf dataset-create/dataset-list/dataset-get` | 评测集管理（创建从 JSON 数组文件灌入） |
| `swf clone-ref --workflow-id` | 克隆已发布工作流到本地 IR 会话（复用优先） |
| `swf plan-apply --file plan.json` | **声明式建图**：一份 JSON 落节点/边/绑定/batch/params |
| `swf plan-schema` | 打印 plan.json 规格（Agent 自检字段） |
| `swf add-node/add-edge/bind` | 逐条增量建图 |
| `swf validate / preview` | 离线校验（唯一 start/end、连通、绑定端口存在）/ 预览 DSL |
| `swf node-debug --sid --node-id` | 无状态单节点调试 |
| `swf upload --sid` | 渲染 IR→DSL 上传成工作流草稿 |
| `swf run --workflow-id [--version -1] [--stream]` | 整图运行（异步；-1 跑草稿；--stream 看实时事件） |

## batch vs plan-apply（易混点）

- **batch** 是**运行期**能力：节点对某个数组输入端口**逐项执行**底层执行器并聚合（`{items,count}`）。
  样例里分类器对 dataset 的 `rows` 逐条跑，就是 batch。
- **plan-apply** 是**构建期**能力：把一份 JSON plan **一次性建成图**（替代逐条 add-node/bind），
  **不是**「批量起 N 个 run」。单图内的批量评测由节点级 batch 覆盖。

## 鉴权

应用内提供**可选** API Key 兜底（`X-API-Key` 头，常量时间比较）：
- `config.auth.api_keys` 为空 → 放行（dev/测试零改动）；配置后校验，失败 401。
- 限流基于令牌桶（`rate_rps`/`rate_burst`）；`/healthz`、`/ping`、SSE 长连接豁免。

> 生产环境企业级鉴权应由网关（SSO/Kani）负责；应用内 API Key 仅为无网关时的自保兜底。

## 开发

```bash
make build      # go build ./...
make test       # go test ./...（单元）
# 集成测试（需 infra-up + migrate-up）：
SWF_TEST_DSN='swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true' \
  go test -tags integration ./...
```
