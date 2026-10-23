-- +goose Up
-- +goose StatementBegin
-- Per-topic folder name (issue #225): the name of the torrent's top folder in
-- the client. 'default' keeps the tracker's name, 'custom' uses folder_name,
-- and 'keep' gives every update the name the previous version has in the
-- client. folder_name_seen is keep mode's memory: the last name the scheduler
-- saw or set, used when the previous torrent is gone from the client.
-- Defaults to 'default' so existing topics deliver exactly as before.
ALTER TABLE topics
    ADD COLUMN folder_name_mode TEXT NOT NULL DEFAULT 'default'
        CHECK (folder_name_mode IN ('default', 'custom', 'keep')),
    ADD COLUMN folder_name      TEXT NULL,
    ADD COLUMN folder_name_seen TEXT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN folder_name_mode,
    DROP COLUMN folder_name,
    DROP COLUMN folder_name_seen;
-- +goose StatementEnd
