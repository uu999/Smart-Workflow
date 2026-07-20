-- name: CreateWorkflowVersion :execresult
INSERT INTO workflow_version (workflow_id, version, dsl, change_log)
VALUES (?, ?, ?, ?);

-- name: GetWorkflowVersion :one
SELECT id, workflow_id, version, dsl, change_log, created_at
FROM workflow_version
WHERE workflow_id = ? AND version = ?;

-- name: ListWorkflowVersions :many
SELECT id, workflow_id, version, change_log, created_at
FROM workflow_version
WHERE workflow_id = ?
ORDER BY version DESC;

-- name: MaxWorkflowVersion :one
SELECT COALESCE(MAX(version), 0) AS max_version
FROM workflow_version
WHERE workflow_id = ?;
