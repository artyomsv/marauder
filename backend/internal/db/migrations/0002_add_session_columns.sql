-- +goose Up
-- +goose StatementBegin
ALTER TABLE tracker_credentials ADD COLUMN session_enc   BYTEA;
ALTER TABLE tracker_credentials ADD COLUMN session_nonce BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tracker_credentials DROP COLUMN session_enc;
ALTER TABLE tracker_credentials DROP COLUMN session_nonce;
-- +goose StatementEnd
