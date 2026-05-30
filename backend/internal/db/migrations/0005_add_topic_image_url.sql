-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics ADD COLUMN image_url TEXT;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics DROP COLUMN image_url;
-- +goose StatementEnd
