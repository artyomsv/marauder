package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Clients repository.
type Clients struct {
	pool *pgxpool.Pool
}

// NewClients constructs the repository.
func NewClients(pool *pgxpool.Pool) *Clients {
	return &Clients{pool: pool}
}

// Create inserts a new client config.
func (r *Clients) Create(ctx context.Context, c *domain.Client) (*domain.Client, error) {
	const q = `
INSERT INTO clients (user_id, client_name, display_name, config_enc, config_nonce, is_default)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, created_at, updated_at`
	err := r.pool.QueryRow(ctx, q,
		c.UserID, c.ClientName, c.DisplayName, c.ConfigEnc, c.ConfigNonce, c.IsDefault,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// GetByID fetches a client by id, scoped to user.
func (r *Clients) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error) {
	const q = `
SELECT id, user_id, client_name, display_name, config_enc, config_nonce,
       is_default, created_at, updated_at
FROM clients WHERE id = $1 AND user_id = $2`
	row := r.pool.QueryRow(ctx, q, id, userID)
	var c domain.Client
	err := row.Scan(&c.ID, &c.UserID, &c.ClientName, &c.DisplayName,
		&c.ConfigEnc, &c.ConfigNonce, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// ListForUser returns all clients for a user.
func (r *Clients) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Client, error) {
	const q = `
SELECT id, user_id, client_name, display_name, config_enc, config_nonce,
       is_default, created_at, updated_at
FROM clients WHERE user_id = $1 ORDER BY display_name ASC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Client
	for rows.Next() {
		var c domain.Client
		if err := rows.Scan(&c.ID, &c.UserID, &c.ClientName, &c.DisplayName,
			&c.ConfigEnc, &c.ConfigNonce, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// Delete removes a client by id.
func (r *Clients) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM clients WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDefault returns the user's default client, if any.
func (r *Clients) GetDefault(ctx context.Context, userID uuid.UUID) (*domain.Client, error) {
	const q = `
SELECT id, user_id, client_name, display_name, config_enc, config_nonce,
       is_default, created_at, updated_at
FROM clients WHERE user_id = $1 AND is_default = true LIMIT 1`
	row := r.pool.QueryRow(ctx, q, userID)
	var c domain.Client
	err := row.Scan(&c.ID, &c.UserID, &c.ClientName, &c.DisplayName,
		&c.ConfigEnc, &c.ConfigNonce, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}
