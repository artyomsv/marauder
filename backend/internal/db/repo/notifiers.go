package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// notifiersPool is the minimal subset of *pgxpool.Pool used by Notifiers.
// Defined as an unexported interface so tests can substitute pgxmock.
type notifiersPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Notifiers repository.
type Notifiers struct {
	pool notifiersPool
}

// NewNotifiers constructs the repository.
func NewNotifiers(pool *pgxpool.Pool) *Notifiers {
	return &Notifiers{pool: pool}
}

const notifierColumns = `id, user_id, notifier_name, display_name, config_enc, config_nonce,
       events, is_default, created_at, updated_at`

// unsetDefaultForType clears is_default on the user's notifiers of the given
// type, optionally excluding one id (uuid.Nil = exclude nothing). Runs inside
// the caller's transaction so the "exactly one default per type" invariant
// holds atomically.
func unsetDefaultForType(ctx context.Context, tx pgx.Tx, userID uuid.UUID, notifierName string, exceptID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE notifiers SET is_default = false, updated_at = now()
         WHERE user_id = $1 AND notifier_name = $2 AND id <> $3 AND is_default`,
		userID, notifierName, exceptID)
	return err
}

// Create inserts a new notifier config. When n.IsDefault is true it first
// clears the same-type default in a transaction.
func (r *Notifiers) Create(ctx context.Context, n *domain.Notifier) (*domain.Notifier, error) {
	const ins = `
INSERT INTO notifiers (user_id, notifier_name, display_name, config_enc, config_nonce, events, is_default)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING id, created_at, updated_at`
	if !n.IsDefault {
		err := r.pool.QueryRow(ctx, ins,
			n.UserID, n.NotifierName, n.DisplayName, n.ConfigEnc, n.ConfigNonce, n.Events, n.IsDefault,
		).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
		return n, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE notifiers SET is_default = false, updated_at = now()
         WHERE user_id = $1 AND notifier_name = $2 AND is_default`,
		n.UserID, n.NotifierName); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, ins,
		n.UserID, n.NotifierName, n.DisplayName, n.ConfigEnc, n.ConfigNonce, n.Events, n.IsDefault,
	).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// GetByID fetches a notifier by id, scoped to user.
func (r *Notifiers) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Notifier, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+notifierColumns+` FROM notifiers WHERE id = $1 AND user_id = $2`, id, userID)
	var n domain.Notifier
	err := row.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
		&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.IsDefault, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

// ListForUser returns all notifiers for a user.
func (r *Notifiers) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+notifierColumns+` FROM notifiers WHERE user_id = $1 ORDER BY display_name ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Notifier
	for rows.Next() {
		var n domain.Notifier
		if err := rows.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
			&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.IsDefault, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// Update overwrites the mutable fields of a notifier. When isDefault is true it
// transactionally clears the same-type default first. Returns ErrNotFound when
// the row is absent. notifierName is the notifier's immutable type (passed by
// the handler from the existing row) so the same-type unset targets the right rows.
func (r *Notifiers) Update(ctx context.Context, id, userID uuid.UUID, notifierName, displayName string,
	events []string, isDefault bool, configEnc, configNonce []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if isDefault {
		if err := unsetDefaultForType(ctx, tx, userID, notifierName, id); err != nil {
			return err
		}
	}
	ct, err := tx.Exec(ctx,
		`UPDATE notifiers SET display_name = $3, events = $4, is_default = $5,
            config_enc = $6, config_nonce = $7, updated_at = now()
         WHERE id = $1 AND user_id = $2`,
		id, userID, displayName, events, isDefault, configEnc, configNonce)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
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
