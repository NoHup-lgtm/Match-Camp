-- +goose Up
ALTER TABLE profile_photos
    DROP CONSTRAINT IF EXISTS profile_photos_position_range,
    ADD CONSTRAINT profile_photos_position_range CHECK (position BETWEEN 0 AND 2);

DELETE FROM profile_photos WHERE position > 2;

-- +goose Down
ALTER TABLE profile_photos
    DROP CONSTRAINT IF EXISTS profile_photos_position_range,
    ADD CONSTRAINT profile_photos_position_range CHECK (position BETWEEN 0 AND 3);
