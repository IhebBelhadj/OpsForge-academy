-- name: InsertSubmission :one
INSERT INTO challenge_submissions (user_id, course_id, item_id, content_hash, language, code, verdict, passed, total,
                                   seconds, xp, result, submitted_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id;

-- name: ItemSubmissions :many
SELECT * FROM challenge_submissions WHERE user_id = $1 AND course_id = $2 AND item_id = $3 ORDER BY id DESC;

-- name: AcceptedSubmissions :many
SELECT * FROM challenge_submissions WHERE user_id = $1 AND verdict = 'accepted' ORDER BY submitted_at, id;
