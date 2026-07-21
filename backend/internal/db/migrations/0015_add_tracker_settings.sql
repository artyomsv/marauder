-- +goose Up
-- +goose StatementBegin

-- Per-tracker domain configuration (issue #126): which domain the plugin
-- uses ("" / NULL = plugin default) and admin-added custom mirror hostnames.
CREATE TABLE tracker_settings (
    tracker_name   TEXT PRIMARY KEY,
    active_domain  TEXT,
    custom_domains JSONB NOT NULL DEFAULT '[]',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tracker_settings;
-- +goose StatementEnd
