package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// settingsPool is the minimal subset of *pgxpool.Pool used by Settings,
// defined as an unexported interface so tests can substitute a fake.
type settingsPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Settings is the repository for the singleton settings row (id = 1).
type Settings struct {
	pool settingsPool
}

// NewSettings constructs the repository.
func NewSettings(pool *pgxpool.Pool) *Settings {
	return &Settings{pool: pool}
}

// GetSonarr reads the Sonarr integration config, decrypting the API key when
// one is stored. The settings row is a guaranteed singleton (seeded by
// migration 0001), so a missing row is a real error, not ErrNotFound.
func (r *Settings) GetSonarr(ctx context.Context, master *crypto.MasterKey) (*domain.SonarrConfig, error) {
	const q = `
SELECT sonarr_enabled, COALESCE(sonarr_url,''),
       sonarr_api_key_enc, sonarr_api_key_nonce,
       sonarr_poll_interval_sec, sonarr_allowed_trackers,
       sonarr_default_client_id, sonarr_default_category, sonarr_default_download_dir,
       sonarr_update_existing, sonarr_owner_user_id, sonarr_last_seen_at
FROM settings WHERE id = 1`
	var c domain.SonarrConfig
	var enc, nonce []byte
	err := r.pool.QueryRow(ctx, q).Scan(
		&c.Enabled, &c.URL, &enc, &nonce,
		&c.PollIntervalSec, &c.AllowedTrackers,
		&c.DefaultClientID, &c.DefaultCategory, &c.DefaultDownloadDir,
		&c.UpdateExisting, &c.OwnerUserID, &c.LastSeenAt,
	)
	if err != nil {
		return nil, fmt.Errorf("settings: read sonarr config: %w", err)
	}
	if len(enc) > 0 && len(nonce) > 0 {
		key, derr := master.DecryptString(enc, nonce)
		if derr != nil {
			return nil, fmt.Errorf("settings: decrypt sonarr api key: %w", derr)
		}
		c.APIKey = key
	}
	if c.AllowedTrackers == nil {
		c.AllowedTrackers = []string{}
	}
	return &c, nil
}

// UpsertSonarr writes the Sonarr config to the singleton row. The API key is
// only re-encrypted and overwritten when c.APIKey is non-empty — an empty
// key means "leave the stored key unchanged" (rotation guard), so the admin
// UI can save other fields without re-entering the secret.
func (r *Settings) UpsertSonarr(ctx context.Context, master *crypto.MasterKey, c *domain.SonarrConfig) error {
	allowed := c.AllowedTrackers
	if allowed == nil {
		allowed = []string{} // NOT NULL column — never write NULL
	}
	q := `
UPDATE settings SET
    sonarr_enabled              = $1,
    sonarr_url                  = NULLIF($2,''),
    sonarr_poll_interval_sec    = $3,
    sonarr_allowed_trackers     = $4,
    sonarr_default_client_id    = $5,
    sonarr_default_category     = $6,
    sonarr_default_download_dir = $7,
    sonarr_update_existing      = $8,
    sonarr_owner_user_id        = $9,
    updated_at                  = now()`
	args := []any{
		c.Enabled, c.URL, c.PollIntervalSec, allowed,
		c.DefaultClientID, c.DefaultCategory, c.DefaultDownloadDir,
		c.UpdateExisting, c.OwnerUserID,
	}
	if c.APIKey != "" {
		enc, nonce, err := master.EncryptString(c.APIKey)
		if err != nil {
			return fmt.Errorf("settings: encrypt sonarr api key: %w", err)
		}
		q += `, sonarr_api_key_enc = $10, sonarr_api_key_nonce = $11`
		args = append(args, enc, nonce)
	}
	q += ` WHERE id = 1`
	if _, err := r.pool.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("settings: upsert sonarr config: %w", err)
	}
	return nil
}

// UpdateSonarrCursor advances the history-poll cursor after a successful poll.
func (r *Settings) UpdateSonarrCursor(ctx context.Context, lastSeen time.Time) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE settings SET sonarr_last_seen_at = $1, updated_at = now() WHERE id = 1`,
		lastSeen,
	); err != nil {
		return fmt.Errorf("settings: update sonarr cursor: %w", err)
	}
	return nil
}
