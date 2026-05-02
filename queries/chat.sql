-- name: IsConversationMember :one
SELECT EXISTS (
    SELECT 1 FROM conversation_members
    WHERE conversation_id = $1 AND user_id = $2
);

-- name: ListConversationMembers :many
SELECT user_id
FROM conversation_members
WHERE conversation_id = $1;

-- name: CreateMessage :one
INSERT INTO messages (conversation_id, sender_user_id, body)
VALUES ($1, $2, $3)
RETURNING id, conversation_id, sender_user_id, body, created_at;

-- name: ListMessages :many
SELECT id, sender_user_id, body, created_at
FROM messages
WHERE conversation_id = $1
ORDER BY created_at ASC
LIMIT 100;
