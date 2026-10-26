-- name: InsertWorkspace :exec
INSERT INTO workspaces (id, user_id, created_at) VALUES ($1, $2, $3);

-- name: WorkspaceOwner :one
SELECT user_id FROM workspaces WHERE id = $1;
