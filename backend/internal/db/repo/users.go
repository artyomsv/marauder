// Package repo holds all SQL queries behind typed repository structs.
//
// The repository layer exclusively returns domain types. SQL, pgx types,
// and error mapping never leak past the package boundary.
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// ErrNotFound is returned when a row lookup misses.
var ErrNotFound = errors.New("not found")

// Users is the repository for the users table.
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers constructs a Users repository.
func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// Count returns the number of users (including disabled ones).
func (r *Users) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// Create inserts a new user and returns the created row.
func (r *Users) Create(ctx context.Context, u *domain.User) (*domain.User, error) {
	const q = `
INSERT INTO users (username, email, password_hash, role, oidc_subject, oidc_issuer)
VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, NULLIF($5,''), NULLIF($6,''))
RETURNING id, created_at, updated_at`
	err := r.pool.QueryRow(ctx, q,
		u.Username, u.Email, u.PasswordHash, string(u.Role),
		u.OIDCSubject, u.OIDCIssuer,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetByID fetches a user by primary key.
func (r *Users) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return r.scanOne(ctx, `WHERE id = $1`, id)
}

// GetByUsername fetches a user by username.
func (r *Users) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return r.scanOne(ctx, `WHERE username = $1`, username)
}

// GetByOIDCSubject fetches a user by OIDC issuer + subject.
func (r *Users) GetByOIDCSubject(ctx context.Context, issuer, subject string) (*domain.User, error) {
	return r.scanOne(ctx, `WHERE oidc_issuer = $1 AND oidc_subject = $2`, issuer, subject)
}

func (r *Users) scanOne(ctx context.Context, where string, args ...any) (*domain.User, error) {
	q := `SELECT id, username, COALESCE(email,''), COALESCE(password_hash,''),
                 role, COALESCE(oidc_subject,''), COALESCE(oidc_issuer,''),
                 is_disabled, created_at, updated_at, last_login_at
          FROM users ` + where
	row := r.pool.QueryRow(ctx, q, args...)
	var u domain.User
	var role string
	var lastLogin *time.Time
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&role, &u.OIDCSubject, &u.OIDCIssuer,
		&u.IsDisabled, &u.CreatedAt, &u.UpdatedAt, &lastLogin,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	u.LastLoginAt = lastLogin
	return &u, nil
}

// UpdateLastLogin stamps the user's last_login_at.
func (r *Users) UpdateLastLogin(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET last_login_at = $2, updated_at = now() WHERE id = $1`, id, t)
	return err
}

// UpdatePasswordHash rotates the user's password hash. Used by the
// "change password" flow in the Settings page. Caller is responsible
// for verifying the current password before calling this.
func (r *Users) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}
