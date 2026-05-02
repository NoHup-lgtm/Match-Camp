-- name: CreateSwipe :exec
INSERT INTO swipes (actor_user_id, target_user_id, action)
VALUES ($1, $2, $3);

-- name: HasReciprocalLike :one
SELECT EXISTS (
    SELECT 1 FROM swipes
    WHERE actor_user_id = $1 AND target_user_id = $2 AND action = 'like'
);

-- name: UpsertMatch :one
INSERT INTO matches (user_low_id, user_high_id)
VALUES ($1, $2)
ON CONFLICT (user_low_id, user_high_id) DO UPDATE SET user_low_id = EXCLUDED.user_low_id
RETURNING id;

-- name: UpsertConversationForMatch :one
INSERT INTO conversations (match_id)
VALUES ($1)
ON CONFLICT (match_id) DO UPDATE SET match_id = EXCLUDED.match_id
RETURNING id;

-- name: AddConversationMember :exec
INSERT INTO conversation_members (conversation_id, user_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListMatchesForUser :many
SELECT m.id AS match_id, c.id AS conversation_id, m.created_at
FROM matches m
JOIN conversations c ON c.match_id = m.id
WHERE m.user_low_id = $1 OR m.user_high_id = $1
ORDER BY m.created_at DESC;

-- name: ListConversationsForUser :many
SELECT c.id AS conversation_id, c.match_id, c.created_at
FROM conversations c
JOIN conversation_members cm ON cm.conversation_id = c.id
WHERE cm.user_id = $1
ORDER BY c.created_at DESC;
