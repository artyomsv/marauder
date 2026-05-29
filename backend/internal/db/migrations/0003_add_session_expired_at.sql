-- +goose Up
-- +goose StatementBegin
ALTER TABLE tracker_credentials ADD COLUMN session_expired_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tracker_credentials DROP COLUMN session_expired_at;
-- +goose StatementEnd
