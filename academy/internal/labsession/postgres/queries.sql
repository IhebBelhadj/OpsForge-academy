-- name: InsertAttempt :one
INSERT INTO attempts (user_id, lab_id, course_id, item_id, content_hash, status, started_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;

-- name: SetPlaces :exec
UPDATE attempts SET namespace = $1, sandbox = $2 WHERE id = $3;

-- name: GetAttempt :one
SELECT * FROM attempts WHERE id = $1;

-- name: ActiveAttempt :one
SELECT * FROM attempts WHERE user_id = $1 AND status IN ('setup', 'running');

-- name: FinishAttempt :exec
UPDATE attempts SET status = $1, finished_at = $2, seconds = $3, xp = $4, error = $5 WHERE id = $6;

-- name: PassedSteps :many
SELECT DISTINCT step_id FROM step_results WHERE attempt_id = $1 AND passed;

-- name: InsertStepResult :exec
INSERT INTO step_results (attempt_id, step_id, passed, detail, at) VALUES ($1, $2, $3, $4, $5);

-- name: ClearSteps :exec
DELETE FROM step_results WHERE attempt_id = $1;

-- name: HintCounts :many
SELECT step_id, COUNT(*) AS shown FROM hints_shown WHERE attempt_id = $1 GROUP BY step_id;

-- name: InsertHint :execrows
INSERT INTO hints_shown (attempt_id, step_id, idx, at) VALUES ($1, $2, $3, $4)
ON CONFLICT (attempt_id, step_id, idx) DO NOTHING;

-- name: CountHint :exec
UPDATE attempts SET hints_used = hints_used + 1 WHERE id = $1;

-- name: PassedAttempts :many
SELECT * FROM attempts WHERE user_id = $1 AND status = 'passed' ORDER BY finished_at, id;

-- name: RecoverSetups :execrows
UPDATE attempts SET status = 'running' WHERE status = 'setup';
