-- name: InsertUser :exec
INSERT INTO users (name, created_at) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING;

-- name: UserByName :one
SELECT id, name, created_at FROM users WHERE name = $1;

-- name: UserIDs :many
SELECT id FROM users ORDER BY id;

-- name: UserByID :one
SELECT id, name, created_at FROM users WHERE id = $1;

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, csrf, created_at, expires_at) VALUES ($1, $2, $3, $4, $5);

-- name: SessionByHash :one
SELECT token_hash, user_id, csrf, created_at, expires_at FROM sessions WHERE token_hash = $1;

-- name: DeleteSessionsExpiredBefore :exec
DELETE FROM sessions WHERE expires_at < $1;
