package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Notifiers repository.
type Notifiers struct {
	pool *pgxpool.Pool
}

// NewNotifiers constructs the repository.
func NewNotifiers(pool *pgxpool.Pool) *Notifiers {
	return &Notifiers{pool: pool}
}

// Create inserts a new notifier config.
func (r *Notifiers) Create(ctx context.Context, n *domain.Notifier) (*domain.Notifier, error) {
	const q = `
INSERT INTO notifiers (user_id, notifier_name, display_name, config_enc, config_nonce, events)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, created_at, updated_at`
	err := r.pool.QueryRow(ctx, q,
		n.UserID, n.NotifierName, n.DisplayName, n.ConfigEnc, n.ConfigNonce, n.Events,
	).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// GetByID fetches a notifier by id, scoped to user.
func (r *Notifiers) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Notifier, error) {
	const q = `
SELECT id, user_id, notifier_name, display_name, config_enc, config_nonce,
       events, created_at, updated_at
FROM notifiers WHERE id = $1 AND user_id = $2`
	row := r.pool.QueryRow(ctx, q, id, userID)
	var n domain.Notifier
	err := row.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
		&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

// ListForUser returns all notifiers for a user.
func (r *Notifiers) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error) {
	const q = `
SELECT id, user_id, notifier_name, display_name, config_enc, config_nonce,
       events, created_at, updated_at
FROM notifiers WHERE user_id = $1 ORDER BY display_name ASC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Notifier
	for rows.Next() {
		var n domain.Notifier
		if err := rows.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
			&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// Delete removes a notifier.
func (r *Notifiers) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM notifiers WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
