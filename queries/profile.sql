-- name: UpsertProfile :exec
INSERT INTO profiles (user_id, bio, course, campus, birth_date)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET
    bio = EXCLUDED.bio,
    course = EXCLUDED.course,
    campus = EXCLUDED.campus,
    birth_date = EXCLUDED.birth_date,
    updated_at = now();

-- name: UpdateProfileVisibility :execrows
UPDATE profiles
SET visible = $2, updated_at = now()
WHERE user_id = $1
  AND char_length(course) > 0
  AND char_length(campus) > 0;

-- name: GetMyProfile :one
SELECT
    u.id,
    u.email,
    u.display_name,
    COALESCE(p.bio, '') AS bio,
    COALESCE(p.course, '') AS course,
    COALESCE(p.campus, '') AS campus,
    p.birth_date,
    COALESCE(p.visible, false) AS visible
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
WHERE u.id = $1;

-- name: ListDiscoveryProfiles :many
SELECT u.id, u.display_name, p.bio, p.course, p.campus, p.birth_date
FROM users u
JOIN profiles p ON p.user_id = u.id
LEFT JOIN profile_preferences pref ON pref.user_id = $1
WHERE p.visible = true
  AND u.id <> $1
  AND NOT EXISTS (
    SELECT 1 FROM swipes sw
    WHERE sw.actor_user_id = $1 AND sw.target_user_id = u.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM blocks b
    WHERE (b.blocker_id = $1 AND b.blocked_id = u.id)
       OR (b.blocker_id = u.id AND b.blocked_id = $1)
  )
  AND (
    pref.min_age IS NULL
    OR p.birth_date IS NULL
    OR EXTRACT(YEAR FROM AGE(p.birth_date)) BETWEEN COALESCE(pref.min_age, 18) AND COALESCE(pref.max_age, 99)
  )
ORDER BY p.updated_at DESC
LIMIT $2 OFFSET $3;

-- name: UpsertPreferences :exec
INSERT INTO profile_preferences (user_id, interested_in, min_age, max_age)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET
    interested_in = EXCLUDED.interested_in,
    min_age       = EXCLUDED.min_age,
    max_age       = EXCLUDED.max_age,
    updated_at    = now();

-- name: GetPreferences :one
SELECT interested_in, min_age, max_age
FROM profile_preferences
WHERE user_id = $1;

-- name: BlockUser :exec
INSERT INTO blocks (blocker_id, blocked_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: UnblockUser :exec
DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2;

-- name: IsBlocked :one
SELECT EXISTS (
    SELECT 1 FROM blocks
    WHERE blocker_id = $1 AND blocked_id = $2
) AS blocked;

-- name: GetPublicProfile :one
SELECT
    u.id,
    u.display_name,
    COALESCE(p.bio, '') AS bio,
    COALESCE(p.course, '') AS course,
    COALESCE(p.campus, '') AS campus,
    p.birth_date,
    COALESCE(p.visible, false) AS visible,
    COALESCE((SELECT url FROM profile_photos WHERE user_id = u.id ORDER BY position ASC LIMIT 1), '')::text AS photo_url
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
WHERE u.id = $1;

-- name: ListProfilePhotos :many
SELECT id, user_id, url, position, created_at
FROM profile_photos
WHERE user_id = $1
ORDER BY position ASC;

-- name: GetProfilePhotoByPosition :one
SELECT id, user_id, url, position, created_at
FROM profile_photos
WHERE user_id = $1 AND position = $2;

-- name: UpsertProfilePhoto :one
INSERT INTO profile_photos (user_id, url, position)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, position) DO UPDATE SET
    url = EXCLUDED.url,
    created_at = now()
RETURNING id, user_id, url, position, created_at;

-- name: DeleteProfilePhotoByPosition :one
DELETE FROM profile_photos
WHERE user_id = $1 AND position = $2
RETURNING id, user_id, url, position, created_at;
