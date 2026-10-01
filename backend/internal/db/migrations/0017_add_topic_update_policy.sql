-- +goose Up
-- +goose StatementBegin
-- Per-topic update policy (issue #205). add_paused_on_update adds every update
-- of the topic to the client paused; only_new_files skips the files the
-- previous version already had. Both default to false so existing topics keep
-- downloading exactly as before.
ALTER TABLE topics
    ADD COLUMN add_paused_on_update BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN only_new_files       BOOLEAN NOT NULL DEFAULT false;
-- The delivered torrent's file list, [{"path": "...", "size": N}, ...], with
-- paths relative to the torrent's top folder. NULL means unknown: a magnet
-- delivery, a row from before this migration, or a list too long to store.
ALTER TABLE topic_deliveries
    ADD COLUMN files JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topic_deliveries
    DROP COLUMN files;
ALTER TABLE topics
    DROP COLUMN add_paused_on_update,
    DROP COLUMN only_new_files;
-- +goose StatementEnd
