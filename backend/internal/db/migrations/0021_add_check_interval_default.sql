-- +goose Up
-- +goose StatementBegin
-- Instance-wide default check interval for topics created without one
-- (issue #250). Materialised onto the topic at create time, so changing it
-- never touches an existing topic. 900 s matches the historical built-in;
-- the range matches topics.MinCheckIntervalSec..MaxCheckIntervalSec.
ALTER TABLE settings
    ADD COLUMN check_interval_sec_default INTEGER NOT NULL DEFAULT 900
        CONSTRAINT settings_check_interval_sec_default_range
        CHECK (check_interval_sec_default BETWEEN 300 AND 604800);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings DROP COLUMN check_interval_sec_default;
-- +goose StatementEnd
