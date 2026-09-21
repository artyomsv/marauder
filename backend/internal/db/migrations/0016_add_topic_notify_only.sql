-- +goose Up
-- +goose StatementBegin
-- Per-topic notify-only mode (issue #184). When notify_only is true the
-- scheduler keeps checking the topic on its normal schedule and announces new
-- releases, but never resolves a client or submits a torrent — so a topic can
-- be watched without a download client configured at all.
-- notify_only_announce_current additionally announces the release already on
-- the page at the topic's FIRST check (and the first after a reset), for users
-- who want confirmation that the watch works. Both default to false: existing
-- topics keep downloading, and adding a topic stays silent.
ALTER TABLE topics
    ADD COLUMN notify_only                   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN notify_only_announce_current  BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN notify_only,
    DROP COLUMN notify_only_announce_current;
-- +goose StatementEnd
