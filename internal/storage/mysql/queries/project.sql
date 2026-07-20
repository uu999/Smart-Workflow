-- name: CreateProject :execresult
INSERT INTO project (project_id, name, description)
VALUES (?, ?, ?);

-- name: GetProjectByProjectID :one
SELECT id, project_id, name, description, created_at, updated_at
FROM project
WHERE project_id = ? AND deleted_at IS NULL;

-- name: ListProjects :many
SELECT id, project_id, name, description, created_at, updated_at
FROM project
WHERE deleted_at IS NULL
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: UpdateProject :execresult
UPDATE project
SET name = ?, description = ?
WHERE project_id = ? AND deleted_at IS NULL;

-- name: SoftDeleteProject :execresult
UPDATE project
SET deleted_at = NOW()
WHERE project_id = ? AND deleted_at IS NULL;
