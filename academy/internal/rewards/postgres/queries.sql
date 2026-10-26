-- name: UserAwards :many
SELECT badge_id, awarded_at FROM user_badges WHERE user_id = $1;

-- name: InsertAward :execrows
INSERT INTO user_badges (user_id, badge_id, awarded_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id, badge_id) DO NOTHING;
