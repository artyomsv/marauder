-- +goose Up
-- +goose StatementBegin
CREATE TABLE sonarr_instances (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   TEXT        NOT NULL,
    enabled                BOOLEAN     NOT NULL DEFAULT false,
    url                    TEXT        NOT NULL DEFAULT '',
    api_key_enc            BYTEA,
    api_key_nonce          BYTEA,
    poll_interval_sec      INTEGER     NOT NULL DEFAULT 900,
    allowed_trackers       TEXT[]      NOT NULL DEFAULT '{}',
    default_client_id      UUID        REFERENCES clients(id) ON DELETE SET NULL,
    default_category       TEXT        NOT NULL DEFAULT '',
    default_download_dir   TEXT        NOT NULL DEFAULT '',
    update_existing        BOOLEAN     NOT NULL DEFAULT false,
    owner_user_id          UUID        REFERENCES users(id)   ON DELETE SET NULL,
    last_seen_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Carry a configured singleton (migration 0009) into one instance, preserving
-- its history cursor so an upgrading user does not re-import past grabs.
INSERT INTO sonarr_instances
    (name, enabled, url, api_key_enc, api_key_nonce, poll_interval_sec,
     allowed_trackers, default_client_id, default_category, default_download_dir,
     update_existing, owner_user_id, last_seen_at)
SELECT 'Sonarr', sonarr_enabled, sonarr_url, sonarr_api_key_enc,
       sonarr_api_key_nonce, sonarr_poll_interval_sec, sonarr_allowed_trackers,
       sonarr_default_client_id, sonarr_default_category,
       sonarr_default_download_dir, sonarr_update_existing,
       sonarr_owner_user_id, sonarr_last_seen_at
FROM settings
WHERE id = 1 AND sonarr_url IS NOT NULL AND sonarr_url <> '';

ALTER TABLE settings
    DROP COLUMN sonarr_enabled,
    DROP COLUMN sonarr_url,
    DROP COLUMN sonarr_api_key_enc,
    DROP COLUMN sonarr_api_key_nonce,
    DROP COLUMN sonarr_poll_interval_sec,
    DROP COLUMN sonarr_allowed_trackers,
    DROP COLUMN sonarr_default_client_id,
    DROP COLUMN sonarr_default_category,
    DROP COLUMN sonarr_default_download_dir,
    DROP COLUMN sonarr_update_existing,
    DROP COLUMN sonarr_owner_user_id,
    DROP COLUMN sonarr_last_seen_at;
-- +goose StatementEnd
-- +goose Down
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

-- Best-effort restore of the most-recently-updated instance into the singleton
-- (lossy for N>1 instances).
UPDATE settings s SET
    sonarr_enabled = i.enabled,
    sonarr_url = i.url,
    sonarr_api_key_enc = i.api_key_enc,
    sonarr_api_key_nonce = i.api_key_nonce,
    sonarr_poll_interval_sec = i.poll_interval_sec,
    sonarr_allowed_trackers = i.allowed_trackers,
    sonarr_default_client_id = i.default_client_id,
    sonarr_default_category = i.default_category,
    sonarr_default_download_dir = i.default_download_dir,
    sonarr_update_existing = i.update_existing,
    sonarr_owner_user_id = i.owner_user_id,
    sonarr_last_seen_at = i.last_seen_at
FROM (SELECT * FROM sonarr_instances ORDER BY updated_at DESC LIMIT 1) i
WHERE s.id = 1;

DROP TABLE sonarr_instances;
-- +goose StatementEnd
