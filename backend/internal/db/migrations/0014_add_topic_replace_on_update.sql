-- +goose Up
-- +goose StatementBegin
-- Per-topic "replace previous version on update" policy (issue #101). When
-- replace_on_update is true and a single-release topic gets a new infohash, the
-- scheduler removes the previously delivered torrent from its client (deleting
-- its on-disk data when replace_delete_data is true) so updated releases don't
-- accumulate duplicate downloads and fill the disk. Default is the historical
-- behaviour (keep every version): opt-in, but delete data once enabled.
ALTER TABLE topics
    ADD COLUMN replace_on_update   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN replace_delete_data BOOLEAN NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN replace_on_update,
    DROP COLUMN replace_delete_data;
-- +goose StatementEnd
