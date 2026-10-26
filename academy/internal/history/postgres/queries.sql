-- name: InsertEvent :one
INSERT INTO events (user_id, kind, subject, data, at) VALUES ($1, $2, $3, $4, $5) RETURNING id;

-- name: EventsAfter :many
SELECT id, user_id, kind, subject, data, at FROM events WHERE user_id = $1 AND id > $2 ORDER BY id LIMIT $3;
