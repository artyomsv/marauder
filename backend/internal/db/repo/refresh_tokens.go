package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// refreshTokensPool is the consumer-side seam over *pgxpool.Pool, so the
// repo is unit-testable with pgxmock (which satisfies this interface).
type refreshTokensPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RefreshTokens repository.
type RefreshTokens struct {
	pool refreshTokensPool
}

// NewRefreshTokens constructs the repository.
func NewRefreshTokens(pool refreshTokensPool) *RefreshTokens {
	return &RefreshTokens{pool: pool}
}

// Insert stores a new refresh token hash for a user.
func (r *RefreshTokens) Insert(ctx context.Context, t *domain.RefreshToken) error {
	const q = `
INSERT INTO refresh_tokens (id, user_id, token_hash, issued_at, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,'')::inet)`
	_, err := r.pool.Exec(ctx, q,
		t.ID, t.UserID, t.TokenHash, t.IssuedAt, t.ExpiresAt, t.UserAgent, t.IP,
	)
	return err
}

// GetByHash looks up a live refresh token.
func (r *RefreshTokens) GetByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	const q = `
SELECT id, user_id, token_hash, issued_at, expires_at, revoked_at, replaced_by,
       COALESCE(user_agent,''), COALESCE(host(ip), '')
FROM refresh_tokens WHERE token_hash = $1`
	row := r.pool.QueryRow(ctx, q, hash)
	var t domain.RefreshToken
	var revoked *time.Time
	var replacedBy *uuid.UUID
	err := row.Scan(
		&t.ID, &t.UserID, &t.TokenHash, &t.IssuedAt, &t.ExpiresAt,
		&revoked, &replacedBy, &t.UserAgent, &t.IP,
	)
	if err != nil {
		return nil, err
	}
	t.RevokedAt = revoked
	t.ReplacedBy = replacedBy
	return &t, nil
}

// Rotate revokes an old token and inserts a new one in a single transaction.
func (r *RefreshTokens) Rotate(ctx context.Context, oldID uuid.UUID, newTok *domain.RefreshToken) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Insert the new token first so its id exists before the old row's
	// replaced_by FK points at it. The refresh_tokens_replaced_by_fkey
	// constraint is checked per-statement (not DEFERRABLE), so the
	// reverse order fails with SQLSTATE 23503.
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, issued_at, expires_at, user_agent, ip)
         VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')::inet)`,
		newTok.ID, newTok.UserID, newTok.TokenHash,
		newTok.IssuedAt, newTok.ExpiresAt, newTok.UserAgent, newTok.IP,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now(), replaced_by = $2 WHERE id = $1`,
		oldID, newTok.ID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Revoke marks a refresh token as revoked.
func (r *RefreshTokens) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, id)
	return err
}

// RevokeAllForUser revokes every active refresh token for a user.
func (r *RefreshTokens) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID,
	)
	return err
}
