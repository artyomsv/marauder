-- +goose Up
-- +goose StatementBegin
-- Instance-wide defaults for the per-topic replace-on-update policy (issue
-- #101) applied to topics created without an explicit choice (issue #239).
-- Materialised onto the topic at create time, so changing them never touches
-- an existing topic. The defaults match the historical behaviour: keep every
-- version; delete data once replace is enabled.
ALTER TABLE settings
    ADD COLUMN replace_on_update_default   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN replace_delete_data_default BOOLEAN NOT NULL DEFAULT true;
-- Per-Sonarr-instance overrides for topics the poller creates. NULL inherits
-- the instance-wide default above; each column inherits independently.
ALTER TABLE sonarr_instances
    ADD COLUMN default_replace_on_update   BOOLEAN NULL,
    ADD COLUMN default_replace_delete_data BOOLEAN NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sonarr_instances
    DROP COLUMN default_replace_delete_data,
    DROP COLUMN default_replace_on_update;
ALTER TABLE settings
    DROP COLUMN replace_delete_data_default,
    DROP COLUMN replace_on_update_default;
-- +goose StatementEnd
