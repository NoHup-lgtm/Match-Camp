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

-- name: ListDiscoveryProfiles :many
SELECT u.id, u.display_name, p.bio, p.course, p.campus
FROM users u
JOIN profiles p ON p.user_id = u.id
WHERE p.visible = true
  AND u.id <> $1
  AND NOT EXISTS (
    SELECT 1 FROM swipes sw
    WHERE sw.actor_user_id = $1 AND sw.target_user_id = u.id
  )
ORDER BY p.updated_at DESC
LIMIT 25;

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
