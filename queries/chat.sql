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
RETURNING id, conversation_id, sender_user_id, body, is_read, created_at;

-- name: ListMessagesPaginated :many
SELECT id, conversation_id, sender_user_id, body, is_read, created_at
FROM messages
WHERE conversation_id = $1
  AND (sqlc.narg('before_created_at')::timestamptz IS NULL OR created_at < sqlc.narg('before_created_at'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_count');

-- name: MarkMessagesRead :exec
UPDATE messages
SET is_read = true
WHERE conversation_id = $1
  AND sender_user_id <> $2
  AND is_read = false;
