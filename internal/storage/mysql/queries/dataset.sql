-- name: CreateDataset :execresult
INSERT INTO dataset (dataset_id, project_id, name, col_schema, row_data, row_count)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetDataset :one
SELECT id, dataset_id, project_id, name, col_schema, row_data, row_count, status, created_at, updated_at
FROM dataset
WHERE dataset_id = ? AND deleted_at IS NULL;

-- name: ListDatasets :many
SELECT id, dataset_id, project_id, name, row_count, status, created_at, updated_at
FROM dataset
WHERE project_id = ? AND deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: SearchDatasets :many
-- 按项目 + 名称模糊搜索（name LIKE，调用方传 %term% 或 % 匹配全部）。
SELECT id, dataset_id, project_id, name, row_count, status, created_at, updated_at
FROM dataset
WHERE project_id = ? AND name LIKE ? AND deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: UpdateDataset :execresult
UPDATE dataset
SET name = ?, col_schema = ?, row_data = ?, row_count = ?
WHERE dataset_id = ? AND deleted_at IS NULL;

-- name: SoftDeleteDataset :execresult
UPDATE dataset
SET deleted_at = NOW()
WHERE dataset_id = ? AND deleted_at IS NULL;
