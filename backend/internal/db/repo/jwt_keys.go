package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JWTKey is a single signing key row.
type JWTKey struct {
	ID              string
	Algo            string
	PrivateKeyEnc   []byte
	PrivateKeyNonce []byte
	PublicKeyPEM    string
	Active          bool
}

// JWTKeys repository.
type JWTKeys struct {
	pool *pgxpool.Pool
}

// NewJWTKeys constructs the repository.
func NewJWTKeys(pool *pgxpool.Pool) *JWTKeys {
	return &JWTKeys{pool: pool}
}

// GetActive returns the currently active signing key, or ErrNotFound.
func (r *JWTKeys) GetActive(ctx context.Context) (*JWTKey, error) {
	const q = `
SELECT id, algo, private_key_enc, private_key_nonce, public_key_pem, active
FROM jwt_keys WHERE active = true LIMIT 1`
	row := r.pool.QueryRow(ctx, q)
	var k JWTKey
	err := row.Scan(&k.ID, &k.Algo, &k.PrivateKeyEnc, &k.PrivateKeyNonce, &k.PublicKeyPEM, &k.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// GetByID looks up a key by its ID (for validating historical tokens).
func (r *JWTKeys) GetByID(ctx context.Context, id string) (*JWTKey, error) {
	const q = `
SELECT id, algo, private_key_enc, private_key_nonce, public_key_pem, active
FROM jwt_keys WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	var k JWTKey
	err := row.Scan(&k.ID, &k.Algo, &k.PrivateKeyEnc, &k.PrivateKeyNonce, &k.PublicKeyPEM, &k.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// InsertActive stores a new key and sets it as the active one, deactivating
// any previous active key. Runs in a single transaction.
func (r *JWTKeys) InsertActive(ctx context.Context, k *JWTKey) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE jwt_keys SET active = false WHERE active = true`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO jwt_keys (id, algo, private_key_enc, private_key_nonce, public_key_pem, active)
VALUES ($1,$2,$3,$4,$5,true)`,
		k.ID, k.Algo, k.PrivateKeyEnc, k.PrivateKeyNonce, k.PublicKeyPEM,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
