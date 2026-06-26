-- +goose Up
-- +goose StatementBegin
ALTER TABLE topic_deliveries ADD COLUMN completed_at TIMESTAMPTZ;
-- +goose StatementEnd
-- +goose StatementBegin
-- Go-forward only: stamp every pre-existing delivery as already accounted for,
-- so enabling the watcher does NOT back-notify "download finished" for torrents
-- that completed before this feature shipped. Only deliveries recorded after
-- this migration are watched. (Mirrors the Sonarr integration's go-forward enable.)
UPDATE topic_deliveries SET completed_at = now() WHERE completed_at IS NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_topic_deliveries_incomplete ON topic_deliveries (delivered_at) WHERE completed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_topic_deliveries_incomplete;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE topic_deliveries DROP COLUMN completed_at;
-- +goose StatementEnd
