package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// sonarrInstancesPool is the minimal subset of *pgxpool.Pool used by
// SonarrInstances, defined as an unexported interface so tests can substitute a
// fake.
type sonarrInstancesPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SonarrInstances is the repository for the sonarr_instances table — one row per
// configured Sonarr instance. The API key is encrypted at rest; the plaintext
// never leaves this boundary except through the decrypted domain.SonarrInstance.
type SonarrInstances struct {
	pool sonarrInstancesPool
}

// NewSonarrInstances constructs the repository.
func NewSonarrInstances(pool *pgxpool.Pool) *SonarrInstances {
	return &SonarrInstances{pool: pool}
}

// sonarrCols is the canonical column list, shared by every SELECT / RETURNING so
// scanInstance can decode any of them.
const sonarrCols = `id, name, enabled, COALESCE(url,''), api_key_enc, api_key_nonce,
       poll_interval_sec, allowed_trackers, default_client_id, default_category,
       default_download_dir, update_existing, owner_user_id, last_seen_at,
       created_at, updated_at`

// scanInstance decodes one row (pgx.Row or pgx.Rows) into a SonarrInstance,
// decrypting the API key. rowScanner is shared with the other repos.
func scanInstance(row rowScanner, master *crypto.MasterKey) (domain.SonarrInstance, error) {
	var inst domain.SonarrInstance
	var enc, nonce []byte
	if err := row.Scan(
		&inst.ID, &inst.Name, &inst.Enabled, &inst.URL, &enc, &nonce,
		&inst.PollIntervalSec, &inst.AllowedTrackers, &inst.DefaultClientID,
		&inst.DefaultCategory, &inst.DefaultDownloadDir, &inst.UpdateExisting,
		&inst.OwnerUserID, &inst.LastSeenAt, &inst.CreatedAt, &inst.UpdatedAt,
	); err != nil {
		return domain.SonarrInstance{}, err
	}
	if len(enc) > 0 && len(nonce) > 0 {
		key, derr := master.DecryptString(enc, nonce)
		if derr != nil {
			return domain.SonarrInstance{}, fmt.Errorf("sonarr_instances: decrypt api key: %w", derr)
		}
		inst.APIKey = key
	}
	if inst.AllowedTrackers == nil {
		inst.AllowedTrackers = []string{}
	}
	return inst, nil
}

// List returns all instances ordered by creation time.
func (r *SonarrInstances) List(ctx context.Context, master *crypto.MasterKey) ([]domain.SonarrInstance, error) {
	return r.query(ctx, master, `SELECT `+sonarrCols+` FROM sonarr_instances ORDER BY created_at`)
}

// ListEnabled returns only enabled instances — the set the poller drives.
func (r *SonarrInstances) ListEnabled(ctx context.Context, master *crypto.MasterKey) ([]domain.SonarrInstance, error) {
	return r.query(ctx, master, `SELECT `+sonarrCols+` FROM sonarr_instances WHERE enabled ORDER BY created_at`)
}

func (r *SonarrInstances) query(ctx context.Context, master *crypto.MasterKey, q string) ([]domain.SonarrInstance, error) {
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sonarr_instances: list: %w", err)
	}
	defer rows.Close()
	out := []domain.SonarrInstance{}
	for rows.Next() {
		inst, serr := scanInstance(rows, master)
		if serr != nil {
			return nil, fmt.Errorf("sonarr_instances: scan: %w", serr)
		}
		out = append(out, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sonarr_instances: rows: %w", err)
	}
	return out, nil
}

// Get reads a single instance by id.
func (r *SonarrInstances) Get(ctx context.Context, master *crypto.MasterKey, id uuid.UUID) (*domain.SonarrInstance, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+sonarrCols+` FROM sonarr_instances WHERE id = $1`, id)
	inst, err := scanInstance(row, master)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sonarr_instances: get: %w", err)
	}
	return &inst, nil
}

// Create inserts a new instance. The API key, when non-empty, is encrypted at
// rest. The database generates the id and timestamps.
func (r *SonarrInstances) Create(ctx context.Context, master *crypto.MasterKey, inst domain.SonarrInstance) (*domain.SonarrInstance, error) {
	allowed := inst.AllowedTrackers
	if allowed == nil {
		allowed = []string{}
	}
	cols := `name, enabled, url, poll_interval_sec, allowed_trackers,
	         default_client_id, default_category, default_download_dir,
	         update_existing, owner_user_id`
	vals := `$1, $2, NULLIF($3,''), $4, $5, $6, $7, $8, $9, $10`
	args := []any{
		inst.Name, inst.Enabled, inst.URL, inst.PollIntervalSec, allowed,
		inst.DefaultClientID, inst.DefaultCategory, inst.DefaultDownloadDir,
		inst.UpdateExisting, inst.OwnerUserID,
	}
	if inst.APIKey != "" {
		enc, nonce, err := master.EncryptString(inst.APIKey)
		if err != nil {
			return nil, fmt.Errorf("sonarr_instances: encrypt api key: %w", err)
		}
		cols += `, api_key_enc, api_key_nonce`
		vals += `, $11, $12`
		args = append(args, enc, nonce)
	}
	q := `INSERT INTO sonarr_instances (` + cols + `) VALUES (` + vals + `) RETURNING ` + sonarrCols
	created, err := scanInstance(r.pool.QueryRow(ctx, q, args...), master)
	if err != nil {
		return nil, fmt.Errorf("sonarr_instances: create: %w", err)
	}
	return &created, nil
}

// Update writes the editable fields of an instance. An empty inst.APIKey leaves
// the stored key unchanged (rotation guard). owner_user_id is set once at create
// and is not modified here.
func (r *SonarrInstances) Update(ctx context.Context, master *crypto.MasterKey, id uuid.UUID, inst domain.SonarrInstance) (*domain.SonarrInstance, error) {
	allowed := inst.AllowedTrackers
	if allowed == nil {
		allowed = []string{}
	}
	q := `
UPDATE sonarr_instances SET
    name                 = $1,
    enabled              = $2,
    url                  = NULLIF($3,''),
    poll_interval_sec    = $4,
    allowed_trackers     = $5,
    default_client_id    = $6,
    default_category     = $7,
    default_download_dir = $8,
    update_existing      = $9,
    updated_at           = now()`
	args := []any{
		inst.Name, inst.Enabled, inst.URL, inst.PollIntervalSec, allowed,
		inst.DefaultClientID, inst.DefaultCategory, inst.DefaultDownloadDir,
		inst.UpdateExisting,
	}
	if inst.APIKey != "" {
		enc, nonce, err := master.EncryptString(inst.APIKey)
		if err != nil {
			return nil, fmt.Errorf("sonarr_instances: encrypt api key: %w", err)
		}
		q += `, api_key_enc = $10, api_key_nonce = $11 WHERE id = $12 RETURNING ` + sonarrCols
		args = append(args, enc, nonce, id)
	} else {
		q += ` WHERE id = $10 RETURNING ` + sonarrCols
		args = append(args, id)
	}
	updated, err := scanInstance(r.pool.QueryRow(ctx, q, args...), master)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sonarr_instances: update: %w", err)
	}
	return &updated, nil
}

// Delete removes an instance.
func (r *SonarrInstances) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM sonarr_instances WHERE id = $1`, id); err != nil {
		return fmt.Errorf("sonarr_instances: delete: %w", err)
	}
	return nil
}

// UpdateCursor advances an instance's history-poll cursor after a successful poll.
func (r *SonarrInstances) UpdateCursor(ctx context.Context, id uuid.UUID, lastSeen time.Time) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE sonarr_instances SET last_seen_at = $1, updated_at = now() WHERE id = $2`,
		lastSeen, id,
	); err != nil {
		return fmt.Errorf("sonarr_instances: update cursor: %w", err)
	}
	return nil
}
