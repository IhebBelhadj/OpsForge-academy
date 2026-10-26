-- name: InsertOperation :exec
INSERT INTO operations (id, user_id, kind, subject, status, created_at) VALUES ($1, $2, $3, $4, $5, $6);

-- name: FinishOperation :exec
UPDATE operations SET status = $1, result = $2, error = $3, finished_at = $4 WHERE id = $5;

-- name: GetOperation :one
SELECT * FROM operations WHERE id = $1;

-- name: FailRunning :execrows
UPDATE operations SET status = 'failed', error = $1, finished_at = $2 WHERE status = 'running';
