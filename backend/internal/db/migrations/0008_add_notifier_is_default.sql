-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifiers ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX uq_notifiers_default_per_type
    ON notifiers (user_id, notifier_name) WHERE is_default;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uq_notifiers_default_per_type;
ALTER TABLE notifiers DROP COLUMN is_default;
-- +goose StatementEnd
