#!/usr/bin/env bash
# 样例第 1 步：种子数据。建 project + dataset(评测集) + 分类器 application(kind=python)。
# 产出的 ID 写入 .ids.env，供 02_run.sh 复用。
#
# 依赖：swf-server 已起（本地进程或容器）；jq；curl；swf CLI 在 PATH（或用 go run ./cmd/swf）。
# 用法：
#   SWF_SERVER_URL=http://127.0.0.1:8080 bash examples/sentiment/01_seed.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVER="${SWF_SERVER_URL:-http://127.0.0.1:8080}"
SWF="${SWF:-swf}" # 可覆盖为 "go run ./cmd/swf"

echo "== server: $SERVER =="

# 依赖检查（失败给可读提示，对齐验收「失败有可读错误」）。
command -v jq  >/dev/null || { echo "需要 jq：brew install jq"; exit 1; }
command -v curl >/dev/null || { echo "需要 curl"; exit 1; }

api() { # api METHOD PATH [JSON_BODY]
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$SERVER$path" -H 'Content-Type: application/json' -d "$body"
  else
    curl -fsS -X "$method" "$SERVER$path"
  fi
}

# 1) 建项目
echo "-- 建 project"
PROJECT_ID=$(api POST /v1/projects '{"name":"情感评测样例","description":"e2e demo"}' | jq -r '.data.project_id')
[[ -n "$PROJECT_ID" && "$PROJECT_ID" != "null" ]] || { echo "建 project 失败"; exit 1; }
echo "   project_id=$PROJECT_ID"

# 2) 建评测集（用 swf CLI 从 dataset.json 灌入）
echo "-- 建 dataset（swf dataset-create）"
DS_OUT=$($SWF dataset-create --server "$SERVER" \
  --project-id "$PROJECT_ID" --name "情感评测集" --file "$HERE/dataset.json")
DATASET_ID=$(echo "$DS_OUT" | jq -r '.data.dataset_id')
[[ -n "$DATASET_ID" && "$DATASET_ID" != "null" ]] || { echo "建 dataset 失败：$DS_OUT"; exit 1; }
echo "   dataset_id=$DATASET_ID (rows=$(echo "$DS_OUT" | jq -r '.data.row_count'))"

# 3) 建分类器 application（kind=python，config.code = classifier.py）
echo "-- 建 classifier application（kind=python）"
# 用 jq 安全地把 classifier.py 内容塞进 config.code（避免手工转义）。
CONFIG=$(jq -Rs '{code: .}' < "$HERE/classifier.py")
APP_BODY=$(jq -n --arg pid "$PROJECT_ID" --argjson cfg "$CONFIG" \
  '{project_id:$pid, name:"情感分类器", kind:"python", config:$cfg}')
APP_ID=$(api POST /v1/applications "$APP_BODY" | jq -r '.data.app_id')
[[ -n "$APP_ID" && "$APP_ID" != "null" ]] || { echo "建 application 失败"; exit 1; }
echo "   app_id=$APP_ID"

# 4) 落地 ID 供 02_run.sh 使用
cat > "$HERE/.ids.env" <<EOF
PROJECT_ID=$PROJECT_ID
DATASET_ID=$DATASET_ID
APP_ID=$APP_ID
EOF
echo "== 种子完成，ID 写入 $HERE/.ids.env =="
