-- +goose Up
ALTER TABLE profile_photos
    ADD CONSTRAINT profile_photos_position_range CHECK (position BETWEEN 0 AND 3),
    ADD CONSTRAINT profile_photos_url_length CHECK (char_length(url) BETWEEN 1 AND 2048);

CREATE INDEX profile_photos_user_position_idx ON profile_photos(user_id, position);

-- +goose Down
DROP INDEX IF EXISTS profile_photos_user_position_idx;
ALTER TABLE profile_photos
    DROP CONSTRAINT IF EXISTS profile_photos_url_length,
    DROP CONSTRAINT IF EXISTS profile_photos_position_range;
