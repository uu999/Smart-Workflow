# 情感评测样例：dataset → 分类器(batch) → 结果合并 → End

端到端演示 Smart-Workflow 的核心能力：**评测集逐条过分类器、聚合统计准召、由 End 输出**。
分类器是 `kind=python` 的 application（规则版，无需外部 LLM，离线可跑）。

## 文件

| 文件 | 作用 |
|---|---|
| [dataset.json](dataset.json) | 6 条情感评测样本（query + 人工 label） |
| [classifier.py](classifier.py) | 分类器代码（进 application 的 config.code；batch 逐条注入 `inputs["item"]`） |
| [plan.json](plan.json) | 声明式建图：dataset→分类器(batch over rows)→合并 code→End（含占位符） |
| [01_seed.sh](01_seed.sh) | 建 project + dataset + 分类器 application，ID 写入 `.ids.env` |
| [02_run.sh](02_run.sh) | 占位符替换→plan-apply 建图→validate→upload→run --stream |

## 跑法

前置：server + **worker** + sidecar 都在线（见根 README「本地进程起」）。
> ⚠️ 关键：run 走异步队列时，**server 与 worker 必须连同一个 Redis**，否则 run 一直 pending
> 却无人执行（本样例真跑时踩过：只起 server 没起 worker → pending）。无 Redis 时 server 用进程内
> dispatcher 执行，单进程即可。

```bash
export SWF_SERVER_URL=http://127.0.0.1:8080
export SWF="go run ./cmd/swf"   # 或把 swf 装进 PATH

bash examples/sentiment/01_seed.sh
bash examples/sentiment/02_run.sh
```

## 预期结果（真实运行验证）

本样例已真跑通（run 状态 `succeeded`，executed_nodes=5）。End 输出形如：

```json
{
  "total": 6,
  "accuracy": 0.8333,
  "details": [
    {"query": "这家店的服务态度非常好，菜品也很棒", "gold": "正面", "pred": "正面", "hit": true},
    {"query": "味道一般般，没有网上说的那么好吃",   "gold": "负面", "pred": "正面", "hit": false},
    ...
  ]
}
```

（规则分类器对 6 条命中 5 条 → accuracy 0.8333；「味道一般般」被规则误判为正面，是预期的规则局限。）

## 这个样例演示了什么

- **dataset 节点**：按 dataset_id 加载评测集行集，输出 `rows`。
- **batch**：分类器对 `rows` 逐条执行（6 行 → 调 6 次 sidecar），聚合成 `items`。
- **application(kind=python)**：委托 sidecar 执行 classifier.py，`sink()` 提交每条的 pred。
- **结果落地**：合并 code 节点统计准召（对齐分层原则——输出由用户在图内决定，非系统代做导出）。
- **End**：绑定 accuracy/total/details 作为整图输出。
