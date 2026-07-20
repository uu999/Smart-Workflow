-- name: CreateWorkflowRun :execresult
INSERT INTO workflow_run (run_id, workflow_id, version, status, trigger_type, input, output)
VALUES (?, ?, ?, ?, ?, ?, JSON_OBJECT());

-- name: GetWorkflowRun :one
SELECT id, run_id, workflow_id, version, status, trigger_type,
       input, output, error, started_at, finished_at, created_at
FROM workflow_run
WHERE run_id = ?;

-- name: UpdateRunStatus :execresult
UPDATE workflow_run
SET status = ?, output = ?, error = ?, started_at = ?, finished_at = ?
WHERE run_id = ?;

-- name: ListRunsByWorkflow :many
SELECT id, run_id, workflow_id, version, status, trigger_type, created_at
FROM workflow_run
WHERE workflow_id = ?
ORDER BY id DESC
LIMIT ? OFFSET ?;

-- name: CreateNodeRun :execresult
INSERT INTO node_run (run_id, node_id, node_type, status, input, output, error, attempt, cost_ms, started_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListNodeRuns :many
SELECT id, run_id, node_id, node_type, status, input, output, error, attempt, cost_ms, started_at, finished_at
FROM node_run
WHERE run_id = ?
ORDER BY id ASC;
