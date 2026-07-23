#!/usr/bin/env bash
# 样例第 2 步：建图→校验→上传→运行→看结果。
# 读 01_seed.sh 产出的 .ids.env，把 plan.json 里的占位符替换成真实 ID，
# 用 plan-apply 声明式建图，再 upload 成工作流草稿，run --stream 实时看每步。
#
# 依赖：01_seed.sh 已跑过；sidecar 在线（分类器 kind=python 走 sidecar 执行）。
# 用法：
#   SWF_SERVER_URL=http://127.0.0.1:8080 bash examples/sentiment/02_run.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER="${SWF_SERVER_URL:-http://127.0.0.1:8080}"
SWF="${SWF:-swf}"

[[ -f "$HERE/.ids.env" ]] || { echo "缺 .ids.env，请先跑 01_seed.sh"; exit 1; }
# shellcheck disable=SC1090
source "$HERE/.ids.env"
echo "== project=$PROJECT_ID dataset=$DATASET_ID app=$APP_ID =="

command -v jq >/dev/null || { echo "需要 jq：brew install jq"; exit 1; }

# 1) 占位符替换 → 生成本次运行用的 plan（临时文件）
PLAN_TMP="$(mktemp -t swf-plan.XXXXXX.json)"
trap 'rm -f "$PLAN_TMP"' EXIT
sed -e "s#PROJECT_ID_PLACEHOLDER#$PROJECT_ID#g" \
    -e "s#DATASET_ID_PLACEHOLDER#$DATASET_ID#g" \
    -e "s#APP_ID_PLACEHOLDER#$APP_ID#g" \
    "$HERE/plan.json" > "$PLAN_TMP"

# 2) 声明式建图（plan-apply 一次性落节点/边/绑定/batch/params）
echo "-- plan-apply 建图"
APPLY_OUT=$($SWF plan-apply --server "$SERVER" --file "$PLAN_TMP")
SID=$(echo "$APPLY_OUT" | jq -r '.data.sid')
[[ -n "$SID" && "$SID" != "null" ]] || { echo "plan-apply 失败：$APPLY_OUT"; exit 1; }
echo "   sid=$SID nodes=$(echo "$APPLY_OUT" | jq -r '.data.node_num') edges=$(echo "$APPLY_OUT" | jq -r '.data.edge_num')"

# 3) 校验（离线，确保图合法：唯一 start/end、连通、绑定端口存在）
echo "-- validate"
$SWF validate --sid "$SID" | jq -r '"   has_error=\(.data.has_error) issues=\(.data.issues|length)"'

# 4) 上传成工作流草稿
echo "-- upload（新建工作流草稿）"
UP_OUT=$($SWF upload --server "$SERVER" --sid "$SID" --description "情感评测样例")
WF_ID=$(echo "$UP_OUT" | jq -r '.data.workflow_id')
[[ -n "$WF_ID" && "$WF_ID" != "null" ]] || { echo "upload 失败：$UP_OUT"; exit 1; }
echo "   workflow_id=$WF_ID"

# 5) 运行草稿（--version -1 跑草稿；--stream 实时看每步事件）
echo "-- run --stream（version=-1 草稿）"
$SWF run --server "$SERVER" --workflow-id "$WF_ID" --version -1 --stream || {
  echo "run 失败：确认 sidecar 在线（make run-sidecar）"; exit 1;
}

echo "== 运行完成。用 GET /runs 看完整结果： =="
echo "   curl -s $SERVER/v1/runs?workflow_id=$WF_ID | jq '.data.items[0]'"
