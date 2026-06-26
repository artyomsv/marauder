-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics
    ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN last_error_code;
-- +goose StatementEnd
