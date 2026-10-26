package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/config"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

// Minimal in-memory fakes for the auth manager's persistence seams, so the
// router can be exercised with real tokens instead of injected claims.
type fakeJWTKeys struct{ active *repo.JWTKey }

// GetActive reports ErrNotFound while no key is stored — the manager then
// generates one, which is the same contract the real store follows.
func (f *fakeJWTKeys) GetActive(context.Context) (*repo.JWTKey, error) {
	if f.active == nil {
		return nil, repo.ErrNotFound
	}
	return f.active, nil
}
func (f *fakeJWTKeys) GetByID(context.Context, string) (*repo.JWTKey, error) {
	return f.GetActive(context.Background())
}
func (f *fakeJWTKeys) InsertActive(_ context.Context, k *repo.JWTKey) error {
	f.active = k
	return nil
}

type fakeRefreshTokens struct{}

func (f *fakeRefreshTokens) Insert(context.Context, *domain.RefreshToken) error { return nil }
func (f *fakeRefreshTokens) GetByHash(context.Context, string) (*domain.RefreshToken, error) {
	return nil, repo.ErrNotFound
}
func (f *fakeRefreshTokens) Rotate(context.Context, uuid.UUID, *domain.RefreshToken) error {
	return nil
}
func (f *fakeRefreshTokens) Revoke(context.Context, uuid.UUID) error           { return nil }
func (f *fakeRefreshTokens) RevokeAllForUser(context.Context, uuid.UUID) error { return nil }

// TestRouter_SettingsWriteStaysAdminOnly guards the wiring, not just the
// handler: instance defaults change what every user's new topics do, so the
// write endpoint must sit behind the admin role. A mis-grouped route in
// router.go would silently expose it.
func TestRouter_SettingsWriteStaysAdminOnly(t *testing.T) {
	ctx := context.Background()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatalf("master key: %v", err)
	}
	mgr, err := auth.NewManager(ctx, auth.ManagerConfig{
		Issuer: "marauder-test", Audience: "marauder-test",
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
		Master: mk, KeysRepo: &fakeJWTKeys{}, TokensRepo: &fakeRefreshTokens{},
	})
	if err != nil {
		t.Fatalf("auth manager: %v", err)
	}

	mint := func(role domain.Role) string {
		t.Helper()
		pair, err := mgr.Issue(ctx, &domain.User{ID: uuid.New(), Username: string(role), Role: role}, "", "")
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}
		return pair.AccessToken
	}

	h := NewRouter(Deps{
		Cfg:     &config.Config{PublicBaseURL: "http://test"},
		Log:     zerolog.Nop(),
		Manager: mgr,
		Master:  mk,
	})

	for _, tt := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "no token is rejected before any handler runs", token: "", want: http.StatusUnauthorized},
		{name: "a regular user cannot write instance defaults", token: mint(domain.RoleUser), want: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings",
				strings.NewReader(`{"default_follow_tracker_title":true}`))
			req.Header.Set("Content-Type", "application/json")
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}
