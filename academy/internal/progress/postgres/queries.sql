-- name: ItemRecords :many
SELECT item_id, content_hash, completed_at, last_opened_at FROM item_progress WHERE user_id = $1 AND course_id = $2;

-- name: CompleteItem :exec
INSERT INTO item_progress (user_id, course_id, item_id, content_hash, completed_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, course_id, item_id) DO UPDATE SET
    content_hash = excluded.content_hash, completed_at = excluded.completed_at;

-- name: OpenItem :exec
INSERT INTO item_progress (user_id, course_id, item_id, last_opened_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, course_id, item_id) DO UPDATE SET last_opened_at = excluded.last_opened_at;

-- name: InsertCourseCompletion :execrows
INSERT INTO course_completions (user_id, course_id, completed_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id, course_id) DO NOTHING;

-- name: CourseCompletions :many
SELECT course_id, completed_at FROM course_completions WHERE user_id = $1;

-- name: GetNote :one
SELECT body, updated_at FROM notes WHERE user_id = $1 AND course_id = $2 AND item_id = $3;

-- name: SaveNote :exec
INSERT INTO notes (user_id, course_id, item_id, body, updated_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, course_id, item_id) DO UPDATE SET body = excluded.body, updated_at = excluded.updated_at;
