-- name: CreateApplication :execresult
INSERT INTO application (app_id, project_id, name, kind, input_schema, output_schema, config)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetApplication :one
SELECT id, app_id, project_id, name, kind, input_schema, output_schema, config, status, created_at, updated_at
FROM application
WHERE app_id = ? AND deleted_at IS NULL;

-- name: ListApplications :many
SELECT id, app_id, project_id, name, kind, status, created_at, updated_at
FROM application
WHERE project_id = ? AND deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: SearchApplications :many
-- 按项目 + 名称模糊搜索（name LIKE，调用方传 %term% 或 % 匹配全部）。
SELECT id, app_id, project_id, name, kind, status, created_at, updated_at
FROM application
WHERE project_id = ? AND name LIKE ? AND deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: UpdateApplication :execresult
UPDATE application
SET name = ?, kind = ?, input_schema = ?, output_schema = ?, config = ?
WHERE app_id = ? AND deleted_at IS NULL;

-- name: SoftDeleteApplication :execresult
UPDATE application
SET deleted_at = NOW()
WHERE app_id = ? AND deleted_at IS NULL;
