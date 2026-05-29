-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics ADD COLUMN category TEXT;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics DROP COLUMN category;
-- +goose StatementEnd
