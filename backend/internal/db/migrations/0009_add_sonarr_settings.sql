-- +goose Up
-- +goose StatementBegin
ALTER TABLE settings
    ADD COLUMN sonarr_enabled              BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN sonarr_url                  TEXT,
    ADD COLUMN sonarr_api_key_enc          BYTEA,
    ADD COLUMN sonarr_api_key_nonce        BYTEA,
    ADD COLUMN sonarr_poll_interval_sec    INTEGER     NOT NULL DEFAULT 900,
    ADD COLUMN sonarr_allowed_trackers     TEXT[]      NOT NULL DEFAULT '{}',
    ADD COLUMN sonarr_default_client_id    UUID        REFERENCES clients(id) ON DELETE SET NULL,
    ADD COLUMN sonarr_default_category     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN sonarr_default_download_dir TEXT        NOT NULL DEFAULT '',
    ADD COLUMN sonarr_update_existing      BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN sonarr_owner_user_id        UUID        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN sonarr_last_seen_at         TIMESTAMPTZ;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings
    DROP COLUMN sonarr_last_seen_at,
    DROP COLUMN sonarr_owner_user_id,
    DROP COLUMN sonarr_update_existing,
    DROP COLUMN sonarr_default_download_dir,
    DROP COLUMN sonarr_default_category,
    DROP COLUMN sonarr_default_client_id,
    DROP COLUMN sonarr_allowed_trackers,
    DROP COLUMN sonarr_poll_interval_sec,
    DROP COLUMN sonarr_api_key_nonce,
    DROP COLUMN sonarr_api_key_enc,
    DROP COLUMN sonarr_url,
    DROP COLUMN sonarr_enabled;
-- +goose StatementEnd
