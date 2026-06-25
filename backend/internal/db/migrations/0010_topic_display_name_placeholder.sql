-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics
    ADD COLUMN display_name_is_placeholder BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN display_name_is_placeholder;
-- +goose StatementEnd
