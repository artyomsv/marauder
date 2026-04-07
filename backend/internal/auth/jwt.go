// Package auth implements JWT issuance, refresh token rotation, and the
// HTTP middleware that guards protected API routes.
//
// The signing key is ES256 (ECDSA P-256). The private key is stored in the
// jwt_keys table, encrypted with the MasterKey. On first start, if no
// active key exists, a fresh one is generated and persisted.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Claims is the payload baked into every access token.
type Claims struct {
	UserID   string `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// TokenPair is the response returned to the client on login/refresh.
type TokenPair struct {
	AccessToken           string    `json:"access_token"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at"`
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
	TokenType             string    `json:"token_type"`
}

// JWTKeyStore is the persistence interface for signing keys.
//
// Production: implemented by *repo.JWTKeys.
// Tests:      implemented by an in-memory fake.
type JWTKeyStore interface {
	GetActive(ctx context.Context) (*repo.JWTKey, error)
	GetByID(ctx context.Context, id string) (*repo.JWTKey, error)
	InsertActive(ctx context.Context, k *repo.JWTKey) error
}

// RefreshTokenStore is the persistence interface for refresh tokens.
//
// Production: implemented by *repo.RefreshTokens.
// Tests:      implemented by an in-memory fake.
type RefreshTokenStore interface {
	Insert(ctx context.Context, t *domain.RefreshToken) error
	GetByHash(ctx context.Context, hash string) (*domain.RefreshToken, error)
	Rotate(ctx context.Context, oldID uuid.UUID, newTok *domain.RefreshToken) error
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// Manager owns the current signing key and issues/validates tokens.
type Manager struct {
	mu sync.RWMutex

	keyID      string
	privateKey *ecdsa.PrivateKey
	publicKey  *ecdsa.PublicKey

	issuer   string
	audience string

	accessTTL  time.Duration
	refreshTTL time.Duration

	master *crypto.MasterKey
	keys   JWTKeyStore
	tokens RefreshTokenStore
}

// ManagerConfig is the settings needed by the manager.
type ManagerConfig struct {
	Issuer     string
	Audience   string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Master     *crypto.MasterKey
	KeysRepo   JWTKeyStore
	TokensRepo RefreshTokenStore
}

// NewManager loads (or creates) the signing key and returns a Manager ready
// to use.
func NewManager(ctx context.Context, cfg ManagerConfig) (*Manager, error) {
	m := &Manager{
		issuer:     cfg.Issuer,
		audience:   cfg.Audience,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
		master:     cfg.Master,
		keys:       cfg.KeysRepo,
		tokens:     cfg.TokensRepo,
	}
	if err := m.loadOrGenerateKey(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) loadOrGenerateKey(ctx context.Context) error {
	k, err := m.keys.GetActive(ctx)
	if err == nil {
		priv, err := m.decryptPriv(k)
		if err != nil {
			return fmt.Errorf("decrypt active jwt key: %w", err)
		}
		m.keyID = k.ID
		m.privateKey = priv
		m.publicKey = &priv.PublicKey
		return nil
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("load active jwt key: %w", err)
	}
	return m.generateAndStoreKey(ctx)
}

func (m *Manager) decryptPriv(k *repo.JWTKey) (*ecdsa.PrivateKey, error) {
	pemBytes, err := m.master.Decrypt(k.PrivateKeyEnc, k.PrivateKeyNonce)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("not PEM")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return priv, nil
}

func (m *Manager) generateAndStoreKey(ctx context.Context) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	privEnc, nonce, err := m.master.Encrypt(privPEM)
	if err != nil {
		return err
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	keyID := uuid.NewString()
	if err := m.keys.InsertActive(ctx, &repo.JWTKey{
		ID:              keyID,
		Algo:            "ES256",
		PrivateKeyEnc:   privEnc,
		PrivateKeyNonce: nonce,
		PublicKeyPEM:    string(pubPEM),
		Active:          true,
	}); err != nil {
		return err
	}

	m.keyID = keyID
	m.privateKey = priv
	m.publicKey = &priv.PublicKey
	return nil
}

// Issue produces an access token + refresh token pair for the given user.
// The refresh token is persisted (hash only) in the database.
func (m *Manager) Issue(ctx context.Context, user *domain.User, userAgent, ip string) (*TokenPair, error) {
	now := time.Now().UTC()

	claims := Claims{
		UserID:   user.ID.String(),
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessTTL)),
			ID:        uuid.NewString(),
		},
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = m.keyID
	access, err := tok.SignedString(m.privateKey)
	if err != nil {
		return nil, err
	}

	refresh, err := crypto.RandomToken(48)
	if err != nil {
		return nil, err
	}
	refreshExp := now.Add(m.refreshTTL)

	if err := m.tokens.Insert(ctx, &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: crypto.HashToken(refresh),
		IssuedAt:  now,
		ExpiresAt: refreshExp,
		UserAgent: userAgent,
		IP:        ip,
	}); err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:           access,
		AccessTokenExpiresAt:  now.Add(m.accessTTL),
		RefreshToken:          refresh,
		RefreshTokenExpiresAt: refreshExp,
		TokenType:             "Bearer",
	}, nil
}

// Parse validates an access token and returns its claims.
func (m *Manager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.publicKey, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience))
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Refresh rotates a refresh token: the old one is revoked, a new pair is
// issued and returned. The presented refresh token must not be already
// revoked or expired.
func (m *Manager) Refresh(ctx context.Context, rawRefresh string, user *domain.User, userAgent, ip string) (*TokenPair, error) {
	hash := crypto.HashToken(rawRefresh)
	tok, err := m.tokens.GetByHash(ctx, hash)
	if err != nil {
		return nil, errors.New("refresh token not recognized")
	}
	if tok.RevokedAt != nil {
		// Reuse of a revoked token: revoke all of this user's tokens as a
		// precaution. This is the OAuth refresh-token-reuse detection pattern.
		_ = m.tokens.RevokeAllForUser(ctx, tok.UserID)
		return nil, errors.New("refresh token reuse detected; all sessions revoked")
	}
	if time.Now().After(tok.ExpiresAt) {
		return nil, errors.New("refresh token expired")
	}
	if tok.UserID != user.ID {
		return nil, errors.New("refresh token does not match user")
	}

	now := time.Now().UTC()
	newRefresh, err := crypto.RandomToken(48)
	if err != nil {
		return nil, err
	}
	newTok := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: crypto.HashToken(newRefresh),
		IssuedAt:  now,
		ExpiresAt: now.Add(m.refreshTTL),
		UserAgent: userAgent,
		IP:        ip,
	}
	if err := m.tokens.Rotate(ctx, tok.ID, newTok); err != nil {
		return nil, err
	}

	// Issue a fresh access token directly (without recording another refresh).
	claims := Claims{
		UserID:   user.ID.String(),
		Username: user.Username,
		Role:     string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessTTL)),
			ID:        uuid.NewString(),
		},
	}
	jtok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	jtok.Header["kid"] = m.keyID
	access, err := jtok.SignedString(m.privateKey)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:           access,
		AccessTokenExpiresAt:  now.Add(m.accessTTL),
		RefreshToken:          newRefresh,
		RefreshTokenExpiresAt: newTok.ExpiresAt,
		TokenType:             "Bearer",
	}, nil
}

// Revoke marks a refresh token as revoked (logout).
func (m *Manager) Revoke(ctx context.Context, rawRefresh string) error {
	hash := crypto.HashToken(rawRefresh)
	tok, err := m.tokens.GetByHash(ctx, hash)
	if err != nil {
		return nil // already gone — treat as success
	}
	return m.tokens.Revoke(ctx, tok.ID)
}

// KeyID exposes the active key's ID (for diagnostics / JWKs).
func (m *Manager) KeyID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.keyID
}

// PeekRefresh looks up a refresh token by its SHA-256 hash without mutating
// anything. Used by the Refresh handler to resolve the owning user before
// calling Manager.Refresh.
func (m *Manager) PeekRefresh(hash string) (*domain.RefreshToken, error) {
	return m.tokens.GetByHash(context.Background(), hash)
}
