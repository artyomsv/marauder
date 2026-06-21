-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics
    ADD COLUMN notifier_id UUID REFERENCES notifiers(id) ON DELETE SET NULL;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics DROP COLUMN notifier_id;
-- +goose StatementEnd
