-- +goose Up
-- +goose StatementBegin

-- Extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users ------------------------------------------------------------------
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        TEXT NOT NULL UNIQUE,
    email           TEXT UNIQUE,
    password_hash   TEXT,
    role            TEXT NOT NULL CHECK (role IN ('admin','user')),
    oidc_subject    TEXT UNIQUE,
    oidc_issuer     TEXT,
    is_disabled     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ
);

CREATE INDEX idx_users_oidc_subject ON users (oidc_subject);

CREATE TABLE refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    replaced_by     UUID REFERENCES refresh_tokens(id),
    user_agent      TEXT,
    ip              INET
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens (token_hash);

-- JWT signing keys (rotatable) -------------------------------------------
CREATE TABLE jwt_keys (
    id              TEXT PRIMARY KEY,
    algo            TEXT NOT NULL,
    private_key_enc BYTEA NOT NULL,
    private_key_nonce BYTEA NOT NULL,
    public_key_pem  TEXT NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tracker plugins & credentials ------------------------------------------
CREATE TABLE tracker_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_name    TEXT NOT NULL,
    username        TEXT,
    secret_enc      BYTEA,
    secret_nonce    BYTEA,
    extra           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, tracker_name)
);

-- Torrent clients --------------------------------------------------------
CREATE TABLE clients (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_name     TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    config_enc      BYTEA NOT NULL,
    config_nonce    BYTEA NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_clients_user ON clients (user_id);

-- Topics -----------------------------------------------------------------
CREATE TABLE topics (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_name        TEXT NOT NULL,
    url                 TEXT NOT NULL,
    display_name        TEXT NOT NULL,
    client_id           UUID REFERENCES clients(id) ON DELETE SET NULL,
    download_dir        TEXT,
    extra               JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_hash           TEXT,
    last_checked_at     TIMESTAMPTZ,
    last_updated_at     TIMESTAMPTZ,
    next_check_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    check_interval_sec  INTEGER NOT NULL DEFAULT 900,
    consecutive_errors  INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','error')),
    last_error          TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, url)
);

CREATE INDEX idx_topics_next_check ON topics (next_check_at) WHERE status = 'active';
CREATE INDEX idx_topics_user        ON topics (user_id);

-- Topic history ----------------------------------------------------------
CREATE TABLE topic_events (
    id              BIGSERIAL PRIMARY KEY,
    topic_id        UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('info','warn','error')),
    message         TEXT,
    data            JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_topic_events_topic ON topic_events (topic_id, created_at DESC);
CREATE INDEX idx_topic_events_user  ON topic_events (user_id, created_at DESC);

-- Notifiers --------------------------------------------------------------
CREATE TABLE notifiers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notifier_name   TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    config_enc      BYTEA NOT NULL,
    config_nonce    BYTEA NOT NULL,
    events          TEXT[] NOT NULL DEFAULT ARRAY['updated','error'],
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifiers_user ON notifiers (user_id);

-- Audit log (append-only) ------------------------------------------------
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    actor           TEXT,
    action          TEXT NOT NULL,
    target_type     TEXT,
    target_id       TEXT,
    result          TEXT NOT NULL CHECK (result IN ('success','failure')),
    ip              INET,
    user_agent      TEXT,
    details         JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_user     ON audit_log (user_id, created_at DESC);
CREATE INDEX idx_audit_log_action   ON audit_log (action, created_at DESC);

-- Singleton settings row -------------------------------------------------
CREATE TABLE settings (
    id                       INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    scheduler_paused         BOOLEAN NOT NULL DEFAULT false,
    default_check_interval   INTEGER NOT NULL DEFAULT 900,
    oidc_enabled             BOOLEAN NOT NULL DEFAULT false,
    oidc_issuer              TEXT,
    oidc_client_id           TEXT,
    oidc_client_secret_enc   BYTEA,
    oidc_client_secret_nonce BYTEA,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO settings (id) VALUES (1) ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS notifiers;
DROP TABLE IF EXISTS topic_events;
DROP TABLE IF EXISTS topics;
DROP TABLE IF EXISTS clients;
DROP TABLE IF EXISTS tracker_credentials;
DROP TABLE IF EXISTS jwt_keys;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS settings;
-- +goose StatementEnd
