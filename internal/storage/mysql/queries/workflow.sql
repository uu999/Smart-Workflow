-- name: CreateWorkflow :execresult
INSERT INTO workflow (workflow_id, project_id, name, description, draft_dsl)
VALUES (?, ?, ?, ?, ?);

-- name: GetWorkflow :one
SELECT id, workflow_id, project_id, name, description, draft_dsl,
       published_ver, status, version_lock, created_at, updated_at
FROM workflow
WHERE workflow_id = ? AND deleted_at IS NULL;

-- name: ListWorkflows :many
SELECT id, workflow_id, project_id, name, description,
       published_ver, status, created_at, updated_at
FROM workflow
WHERE project_id = ? AND deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: UpdateWorkflowDraft :execresult
UPDATE workflow
SET name = ?, description = ?, draft_dsl = ?, version_lock = version_lock + 1
WHERE workflow_id = ? AND version_lock = ? AND deleted_at IS NULL;

-- name: PublishWorkflow :execresult
UPDATE workflow
SET published_ver = ?, status = 1, version_lock = version_lock + 1
WHERE workflow_id = ? AND deleted_at IS NULL;

-- name: SoftDeleteWorkflow :execresult
UPDATE workflow
SET deleted_at = NOW()
WHERE workflow_id = ? AND deleted_at IS NULL;
