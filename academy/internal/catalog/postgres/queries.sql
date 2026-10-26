-- name: AllContent :many
SELECT kind, key, pack_id, course_id, position, title, content_hash, doc, retired FROM content ORDER BY kind, key;

-- name: UpsertContent :exec
INSERT INTO content (kind, key, pack_id, course_id, position, title, content_hash, doc, retired, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, $9)
ON CONFLICT (kind, key) DO UPDATE SET
    pack_id = excluded.pack_id, course_id = excluded.course_id, position = excluded.position, title = excluded.title,
    content_hash = excluded.content_hash, doc = excluded.doc, retired = false, updated_at = excluded.updated_at;

-- name: RetireContent :exec
UPDATE content SET retired = true, updated_at = $1 WHERE kind = $2 AND key = $3;

-- name: ContentUpdatedAt :one
SELECT updated_at FROM content WHERE kind = $1 AND key = $2;

-- name: LiveContent :one
SELECT kind, key, pack_id, course_id, position, title, content_hash, doc, retired FROM content
WHERE kind = $1 AND key = $2 AND NOT retired;

-- name: LiveContentOfKind :many
SELECT kind, key, pack_id, course_id, position, title, content_hash, doc, retired FROM content
WHERE kind = $1 AND NOT retired ORDER BY pack_id, position, key;

-- name: ContentOfKind :many
SELECT kind, key, pack_id, course_id, position, title, content_hash, doc, retired FROM content
WHERE kind = $1 ORDER BY pack_id, position, key;
