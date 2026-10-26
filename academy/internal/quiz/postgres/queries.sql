-- name: InsertAttempt :one
INSERT INTO quiz_attempts (user_id, course_id, item_id, content_hash, started_at, deadline, questions, pass, offered)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id;

-- name: GetAttempt :one
SELECT * FROM quiz_attempts WHERE id = $1;

-- name: SubmitAttempt :execrows
UPDATE quiz_attempts SET submitted_at = $1, answers = $2, correct = $3, score = $4, passed = $5, xp = $6
WHERE id = $7 AND submitted_at IS NULL;

-- name: ItemAttempts :many
SELECT * FROM quiz_attempts WHERE user_id = $1 AND course_id = $2 AND item_id = $3 ORDER BY id DESC;

-- name: PassedAttempts :many
SELECT * FROM quiz_attempts WHERE user_id = $1 AND passed ORDER BY submitted_at, id;
