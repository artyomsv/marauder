package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// --- Fakes for the repos --------------------------------------------

type fakeJWTKeys struct {
	mu  sync.Mutex
	row *repo.JWTKey
}

func (f *fakeJWTKeys) GetActive(_ context.Context) (*repo.JWTKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row == nil {
		return nil, repo.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeJWTKeys) InsertActive(_ context.Context, k *repo.JWTKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row = k
	return nil
}

func (f *fakeJWTKeys) GetByID(_ context.Context, id string) (*repo.JWTKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.row != nil && f.row.ID == id {
		return f.row, nil
	}
	return nil, repo.ErrNotFound
}

type fakeRefreshTokens struct {
	mu     sync.Mutex
	tokens map[string]*domain.RefreshToken // keyed by token_hash
}

func newFakeRefresh() *fakeRefreshTokens {
	return &fakeRefreshTokens{tokens: map[string]*domain.RefreshToken{}}
}

func (f *fakeRefreshTokens) Insert(_ context.Context, t *domain.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[t.TokenHash] = t
	return nil
}

func (f *fakeRefreshTokens) GetByHash(_ context.Context, h string) (*domain.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[h]
	if !ok {
		return nil, errors.New("not found")
	}
	return t, nil
}

func (f *fakeRefreshTokens) Rotate(_ context.Context, oldID uuid.UUID, newTok *domain.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.ID == oldID {
			now := time.Now()
			t.RevokedAt = &now
			t.ReplacedBy = &newTok.ID
		}
	}
	f.tokens[newTok.TokenHash] = newTok
	return nil
}

func (f *fakeRefreshTokens) Revoke(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.ID == id {
			now := time.Now()
			t.RevokedAt = &now
			return nil
		}
	}
	return nil
}

func (f *fakeRefreshTokens) RevokeAllForUser(_ context.Context, uid uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, t := range f.tokens {
		if t.UserID == uid {
			t.RevokedAt = &now
		}
	}
	return nil
}

// --- Helpers --------------------------------------------------------

func newTestManager(t *testing.T) (*Manager, *fakeJWTKeys, *fakeRefreshTokens, *crypto.MasterKey) {
	t.Helper()
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	master, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(buf))
	if err != nil {
		t.Fatal(err)
	}
	keys := &fakeJWTKeys{}
	tokens := newFakeRefresh()
	mgr, err := NewManager(context.Background(), ManagerConfig{
		Issuer:     "https://test.marauder.cc",
		Audience:   "marauder-api",
		AccessTTL:  10 * time.Minute,
		RefreshTTL: 24 * time.Hour,
		Master:     master,
		KeysRepo:   keys,
		TokensRepo: tokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mgr, keys, tokens, master
}

// --- Tests ----------------------------------------------------------

func TestNewManagerGeneratesKeyOnFirstRun(t *testing.T) {
	_, keys, _, _ := newTestManager(t)

	if keys.row == nil {
		t.Fatal("expected key to be persisted")
	}
	if keys.row.Algo != "ES256" {
		t.Fatalf("want ES256, got %s", keys.row.Algo)
	}
	// Public key PEM must parse
	block, _ := pem.Decode([]byte(keys.row.PublicKeyPEM))
	if block == nil {
		t.Fatal("public key is not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	if _, ok := pub.(*ecdsa.PublicKey); !ok {
		t.Fatalf("want ECDSA public key, got %T", pub)
	}
}

func TestNewManagerReusesExistingKey(t *testing.T) {
	mgr1, keys, _, master := newTestManager(t)
	firstID := mgr1.KeyID()

	// Build a fresh manager that should pick up the same key.
	tokens := newFakeRefresh()
	mgr2, err := NewManager(context.Background(), ManagerConfig{
		Issuer:     "https://test.marauder.cc",
		Audience:   "marauder-api",
		AccessTTL:  10 * time.Minute,
		RefreshTTL: 24 * time.Hour,
		Master:     master,
		KeysRepo:   keys,
		TokensRepo: tokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mgr2.KeyID() != firstID {
		t.Fatalf("expected same key id, got %s vs %s", mgr2.KeyID(), firstID)
	}
}

func TestIssueAndParseRoundTrip(t *testing.T) {
	mgr, _, tokens, _ := newTestManager(t)

	user := &domain.User{
		ID:       uuid.New(),
		Username: "alice",
		Role:     domain.RoleAdmin,
	}
	pair, err := mgr.Issue(context.Background(), user, "test-ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("empty access token")
	}
	if pair.RefreshToken == "" {
		t.Fatal("empty refresh token")
	}

	claims, err := mgr.Parse(pair.AccessToken)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.UserID != user.ID.String() {
		t.Fatalf("uid mismatch: %s vs %s", claims.UserID, user.ID)
	}
	if claims.Username != "alice" {
		t.Fatalf("username mismatch: %s", claims.Username)
	}
	if claims.Role != "admin" {
		t.Fatalf("role mismatch: %s", claims.Role)
	}
	if claims.Issuer != "https://test.marauder.cc" {
		t.Fatalf("issuer mismatch: %s", claims.Issuer)
	}

	// The refresh token must be in the fake repo (as a hash).
	if len(tokens.tokens) != 1 {
		t.Fatalf("want 1 stored refresh token, got %d", len(tokens.tokens))
	}
}

func TestParseRejectsTamperedToken(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	user := &domain.User{
		ID:       uuid.New(),
		Username: "bob",
		Role:     domain.RoleUser,
	}
	pair, err := mgr.Issue(context.Background(), user, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}
	// Flip a few characters in the middle of the token
	tampered := pair.AccessToken[:len(pair.AccessToken)-5] + "xxxxx"
	if _, err := mgr.Parse(tampered); err == nil {
		t.Fatal("expected tampered token to fail validation")
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	mgr, _, tokens, _ := newTestManager(t)
	user := &domain.User{
		ID:       uuid.New(),
		Username: "alice",
		Role:     domain.RoleUser,
	}
	pair, err := mgr.Issue(context.Background(), user, "ua", "ip")
	if err != nil {
		t.Fatal(err)
	}

	newPair, err := mgr.Refresh(context.Background(), pair.RefreshToken, user, "ua", "ip")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if newPair.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token should change")
	}
	if newPair.AccessToken == pair.AccessToken {
		t.Fatal("access token should change")
	}

	// Old token must be revoked
	oldHash := crypto.HashToken(pair.RefreshToken)
	tokens.mu.Lock()
	old := tokens.tokens[oldHash]
	tokens.mu.Unlock()
	if old.RevokedAt == nil {
		t.Fatal("old token should be revoked")
	}
}

func TestRefreshDetectsReuse(t *testing.T) {
	mgr, _, tokens, _ := newTestManager(t)
	user := &domain.User{
		ID:       uuid.New(),
		Username: "alice",
		Role:     domain.RoleUser,
	}
	pair, _ := mgr.Issue(context.Background(), user, "ua", "ip")

	// First refresh succeeds
	if _, err := mgr.Refresh(context.Background(), pair.RefreshToken, user, "ua", "ip"); err != nil {
		t.Fatal(err)
	}
	// Second use of the same (now-revoked) token must error AND
	// revoke all this user's tokens.
	if _, err := mgr.Refresh(context.Background(), pair.RefreshToken, user, "ua", "ip"); err == nil {
		t.Fatal("expected reuse detection")
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	for _, t2 := range tokens.tokens {
		if t2.UserID == user.ID && t2.RevokedAt == nil {
			t.Fatal("expected all of user's tokens to be revoked after reuse")
		}
	}
}

func TestRevoke(t *testing.T) {
	mgr, _, tokens, _ := newTestManager(t)
	user := &domain.User{
		ID:       uuid.New(),
		Username: "alice",
		Role:     domain.RoleUser,
	}
	pair, _ := mgr.Issue(context.Background(), user, "ua", "ip")

	if err := mgr.Revoke(context.Background(), pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tok := tokens.tokens[crypto.HashToken(pair.RefreshToken)]
	if tok == nil || tok.RevokedAt == nil {
		t.Fatal("expected revoke to mark the token")
	}
}

// Compile-time assertions: both the fake and the production repo
// satisfy the interfaces the manager depends on.
var (
	_ JWTKeyStore       = (*fakeJWTKeys)(nil)
	_ RefreshTokenStore = (*fakeRefreshTokens)(nil)
	_ JWTKeyStore       = (*repo.JWTKeys)(nil)
	_ RefreshTokenStore = (*repo.RefreshTokens)(nil)
)
