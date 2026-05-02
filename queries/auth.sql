-- name: CreatePasswordUser :one
INSERT INTO users (email, display_name, password_hash)
VALUES ($1, $2, $3)
RETURNING id, email, display_name;

-- name: GetUserForLogin :one
SELECT id, email, display_name, password_hash
FROM users
WHERE email = $1;

-- name: CreateSession :exec
INSERT INTO sessions (user_id, token_hash, expires_at)
VALUES ($1, $2, $3);

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions
WHERE token_hash = $1;

-- name: GetUserBySessionTokenHash :one
SELECT u.id, u.email, u.display_name
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: GetGoogleUserBySubject :one
SELECT u.id, u.email, u.display_name
FROM auth_identities ai
JOIN users u ON u.id = ai.user_id
WHERE ai.provider = 'google' AND ai.provider_subject = $1;

-- name: UpsertGoogleUserByEmail :one
INSERT INTO users (email, display_name)
VALUES ($1, $2)
ON CONFLICT (email) DO UPDATE SET
    display_name = users.display_name
RETURNING id, email, display_name;

-- name: LinkGoogleIdentity :exec
INSERT INTO auth_identities (user_id, provider, provider_subject, email)
VALUES ($1, 'google', $2, $3)
ON CONFLICT (provider, provider_subject) DO NOTHING;
