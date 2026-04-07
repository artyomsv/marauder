package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// TrackerCredentials is the repository for the tracker_credentials table.
//
// The table has a UNIQUE (user_id, tracker_name) constraint, so each
// user can hold at most one credential per tracker plugin.
type TrackerCredentials struct {
	pool *pgxpool.Pool
}

// NewTrackerCredentials constructs the repository.
func NewTrackerCredentials(pool *pgxpool.Pool) *TrackerCredentials {
	return &TrackerCredentials{pool: pool}
}

// Create inserts a new credential.
func (r *TrackerCredentials) Create(ctx context.Context, c *domain.TrackerCredential) (*domain.TrackerCredential, error) {
	const q = `
INSERT INTO tracker_credentials (user_id, tracker_name, username, secret_enc, secret_nonce, extra)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, created_at, updated_at`
	extra, _ := json.Marshal(c.Extra)
	if len(extra) == 0 {
		extra = []byte("{}")
	}
	err := r.pool.QueryRow(ctx, q,
		c.UserID, c.TrackerName, c.Username, c.SecretEnc, c.SecretNonce, extra,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// GetByID fetches a credential by primary key, scoped to user.
func (r *TrackerCredentials) GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.TrackerCredential, error) {
	return r.scanOne(ctx, `WHERE id = $1 AND user_id = $2`, id, userID)
}

// GetForTracker returns the user's credential for a specific tracker
// plugin, or ErrNotFound if none is set. Used by the scheduler before
// invoking a topic check.
func (r *TrackerCredentials) GetForTracker(ctx context.Context, userID uuid.UUID, trackerName string) (*domain.TrackerCredential, error) {
	return r.scanOne(ctx, `WHERE user_id = $1 AND tracker_name = $2`, userID, trackerName)
}

// ListForUser returns every credential the user has stored, ordered by
// tracker name. Secrets stay encrypted — the caller decrypts only when
// it actually needs to invoke Login.
func (r *TrackerCredentials) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.TrackerCredential, error) {
	const q = `
SELECT id, user_id, tracker_name, COALESCE(username,''), secret_enc, secret_nonce,
       extra, created_at, updated_at
FROM tracker_credentials
WHERE user_id = $1
ORDER BY tracker_name ASC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.TrackerCredential
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Update overwrites the username + secret blob for an existing
// credential. Used by password rotation.
func (r *TrackerCredentials) Update(ctx context.Context, id, userID uuid.UUID,
	username string, secretEnc, secretNonce []byte) error {
	const q = `
UPDATE tracker_credentials
SET username     = $3,
    secret_enc   = $4,
    secret_nonce = $5,
    updated_at   = now()
WHERE id = $1 AND user_id = $2`
	ct, err := r.pool.Exec(ctx, q, id, userID, username, secretEnc, secretNonce)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a credential.
func (r *TrackerCredentials) Delete(ctx context.Context, id, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM tracker_credentials WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *TrackerCredentials) scanOne(ctx context.Context, where string, args ...any) (*domain.TrackerCredential, error) {
	q := `SELECT id, user_id, tracker_name, COALESCE(username,''), secret_enc, secret_nonce,
                 extra, created_at, updated_at
          FROM tracker_credentials ` + where
	row := r.pool.QueryRow(ctx, q, args...)
	c, err := scanCred(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// rowScanner is the minimal interface implemented by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCred(s rowScanner) (*domain.TrackerCredential, error) {
	var c domain.TrackerCredential
	var extraRaw []byte
	err := s.Scan(
		&c.ID, &c.UserID, &c.TrackerName, &c.Username,
		&c.SecretEnc, &c.SecretNonce, &extraRaw,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(extraRaw) > 0 {
		_ = json.Unmarshal(extraRaw, &c.Extra)
	}
	if c.Extra == nil {
		c.Extra = map[string]any{}
	}
	return &c, nil
}
