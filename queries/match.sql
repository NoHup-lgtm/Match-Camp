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
SELECT
    m.id                                                     AS match_id,
    c.id                                                     AS conversation_id,
    m.created_at,
    pu.id                                                                                         AS partner_id,
    pu.display_name                                                                               AS partner_display_name,
    pp.birth_date                                                                                 AS partner_birth_date,
    COALESCE((SELECT url FROM profile_photos WHERE user_id = pu.id ORDER BY position ASC LIMIT 1), '')::text AS partner_photo_url,
    COALESCE(msg.body, '')                                                                        AS last_message_body,
    COALESCE(msg.sender_user_id, '00000000-0000-0000-0000-000000000000'::uuid)                   AS last_message_sender_id,
    msg.created_at                                                                                AS last_message_at
FROM matches m
JOIN conversations c ON c.match_id = m.id
JOIN users pu ON (
    (m.user_low_id = $1 AND pu.id = m.user_high_id) OR
    (m.user_high_id = $1 AND pu.id = m.user_low_id)
)
LEFT JOIN profiles pp ON pp.user_id = pu.id
LEFT JOIN LATERAL (
    SELECT body, sender_user_id, created_at
    FROM messages
    WHERE conversation_id = c.id
    ORDER BY created_at DESC
    LIMIT 1
) msg ON true
WHERE m.user_low_id = $1 OR m.user_high_id = $1
ORDER BY COALESCE(msg.created_at, m.created_at) DESC;

-- name: ListConversationsForUser :many
SELECT
    c.id                                                     AS conversation_id,
    c.match_id,
    c.created_at,
    pu.id                                                                                         AS partner_id,
    pu.display_name                                                                               AS partner_display_name,
    pp.birth_date                                                                                 AS partner_birth_date,
    COALESCE((SELECT url FROM profile_photos WHERE user_id = pu.id ORDER BY position ASC LIMIT 1), '')::text AS partner_photo_url,
    COALESCE(msg.body, '')                                                                        AS last_message_body,
    COALESCE(msg.sender_user_id, '00000000-0000-0000-0000-000000000000'::uuid)                   AS last_message_sender_id,
    msg.created_at                                                                                AS last_message_at,
    (SELECT COUNT(*)::int FROM messages m2
     WHERE m2.conversation_id = c.id AND m2.sender_user_id <> $1 AND m2.is_read = false) AS unread_count
FROM conversations c
JOIN conversation_members cm  ON cm.conversation_id = c.id AND cm.user_id = $1
JOIN conversation_members cm2 ON cm2.conversation_id = c.id AND cm2.user_id <> $1
JOIN users pu ON pu.id = cm2.user_id
LEFT JOIN profiles pp ON pp.user_id = pu.id
LEFT JOIN LATERAL (
    SELECT body, sender_user_id, created_at
    FROM messages
    WHERE conversation_id = c.id
    ORDER BY created_at DESC
    LIMIT 1
) msg ON true
ORDER BY COALESCE(msg.created_at, c.created_at) DESC;
