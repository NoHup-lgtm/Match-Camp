-- +goose Up

ALTER TABLE messages ADD COLUMN is_read boolean NOT NULL DEFAULT false;
ALTER TABLE conversations ADD COLUMN last_message_at timestamptz;

CREATE INDEX messages_conversation_unread_idx ON messages(conversation_id) WHERE is_read = false;

-- Trigger para manter last_message_at atualizado automaticamente.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fn_update_conversation_last_message_at()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE conversations SET last_message_at = NEW.created_at WHERE id = NEW.conversation_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_messages_update_last_message_at
AFTER INSERT ON messages
FOR EACH ROW EXECUTE FUNCTION fn_update_conversation_last_message_at();

-- +goose Down

DROP TRIGGER IF EXISTS trg_messages_update_last_message_at ON messages;
DROP FUNCTION IF EXISTS fn_update_conversation_last_message_at;
DROP INDEX IF EXISTS messages_conversation_unread_idx;
ALTER TABLE conversations DROP COLUMN IF EXISTS last_message_at;
ALTER TABLE messages DROP COLUMN IF EXISTS is_read;
