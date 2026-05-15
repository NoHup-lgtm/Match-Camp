-- +goose Up

-- Tabela de bloqueios entre usuários.
CREATE TABLE blocks (
    blocker_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);

CREATE INDEX blocks_blocked_id_idx ON blocks(blocked_id);

-- +goose Down

DROP INDEX IF EXISTS blocks_blocked_id_idx;
DROP TABLE IF EXISTS blocks;
