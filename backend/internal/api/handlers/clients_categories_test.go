package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// plainClient implements registry.Client only (no WithCategories).
type plainClient struct{ name string }

func (c *plainClient) Name() string                 { return c.name }
func (c *plainClient) DisplayName() string          { return c.name }
func (c *plainClient) ConfigSchema() map[string]any { return nil }
func (c *plainClient) Test(context.Context, []byte) error {
	return nil
}
func (c *plainClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}

// categoriesClient additionally implements registry.WithCategories.
type categoriesClient struct {
	plainClient
	names []string
	err   error
}

func (c *categoriesClient) Categories(context.Context, []byte) ([]string, error) {
	return c.names, c.err
}

func TestResolveCategories(t *testing.T) {
	tests := []struct {
		name          string
		plugin        registry.Client
		wantSupported bool
		wantNames     []string
	}{
		{
			name:          "nil plugin is unsupported",
			plugin:        nil,
			wantSupported: false,
			wantNames:     nil,
		},
		{
			name:          "plugin without WithCategories is unsupported",
			plugin:        &plainClient{name: "transmission"},
			wantSupported: false,
			wantNames:     nil,
		},
		{
			name:          "supported plugin returns its names",
			plugin:        &categoriesClient{plainClient: plainClient{name: "qbittorrent"}, names: []string{"Movies", "TV"}},
			wantSupported: true,
			wantNames:     []string{"Movies", "TV"},
		},
		{
			name:          "supported plugin error fails open to empty",
			plugin:        &categoriesClient{plainClient: plainClient{name: "qbittorrent"}, err: errors.New("unreachable")},
			wantSupported: true,
			wantNames:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			supported, names := resolveCategories(context.Background(), tt.plugin, nil, zerolog.Nop())
			if supported != tt.wantSupported {
				t.Errorf("supported = %v, want %v", supported, tt.wantSupported)
			}
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestCategoriesView_NilInput_ReturnsNonNilEmptySlice(t *testing.T) {
	v := categoriesView(false, nil)
	cats, ok := v["categories"].([]string)
	if !ok {
		t.Fatalf("categories not a []string: %T", v["categories"])
	}
	if cats == nil || len(cats) != 0 {
		t.Errorf("categories = %v, want non-nil empty slice", cats)
	}
	if v["supported"] != false {
		t.Errorf("supported = %v, want false", v["supported"])
	}
}

// --- HTTP-level tests for the Categories handler -----------------------

// fakeClientStore is a clientStore whose GetByID is configurable; the other
// methods are unused by the Categories handler.
type fakeClientStore struct {
	client *domain.Client
	err    error
}

func (f *fakeClientStore) ListForUser(context.Context, uuid.UUID) ([]*domain.Client, error) {
	return nil, nil
}
func (f *fakeClientStore) Create(context.Context, *domain.Client) (*domain.Client, error) {
	return nil, nil
}
func (f *fakeClientStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Client, error) {
	return f.client, f.err
}
func (f *fakeClientStore) Update(context.Context, uuid.UUID, uuid.UUID, string, bool, []byte, []byte) error {
	return nil
}
func (f *fakeClientStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// recordingCryptor is a passthrough cryptor that records whether Decrypt was
// called, so a test can assert an unsupported client never decrypts its config.
type recordingCryptor struct{ decrypted bool }

func (c *recordingCryptor) Encrypt(pt []byte) ([]byte, []byte, error) { return pt, nil, nil }
func (c *recordingCryptor) Decrypt(ct, _ []byte) ([]byte, error) {
	c.decrypted = true
	return ct, nil
}

// Register fake client plugins once for the HTTP handler tests. Names are
// unique so they never collide with real registered plugins.
const (
	plainCatClientName = "plaincattest"
	withCatClientName  = "withcategoriestest"
)

func init() {
	registry.RegisterClient(&plainClient{name: plainCatClientName})
	registry.RegisterClient(&categoriesClient{
		plainClient: plainClient{name: withCatClientName},
		names:       []string{"Movies", "TV"},
	})
}

func TestClients_Categories_HTTP(t *testing.T) {
	uid := uuid.New()
	cid := uuid.New()

	tests := []struct {
		name           string
		store          *fakeClientStore
		wantStatus     int
		wantSupported  bool
		wantCategories []string
		wantDecrypted  bool
	}{
		{
			name:       "unknown client id returns 404",
			store:      &fakeClientStore{err: repo.ErrNotFound},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unsupported plugin returns supported:false without decrypting",
			store: &fakeClientStore{client: &domain.Client{
				ID: cid, ClientName: plainCatClientName, ConfigEnc: []byte("{}"),
			}},
			wantStatus:     http.StatusOK,
			wantSupported:  false,
			wantCategories: []string{},
			wantDecrypted:  false,
		},
		{
			name: "supported plugin returns its categories",
			store: &fakeClientStore{client: &domain.Client{
				ID: cid, ClientName: withCatClientName, ConfigEnc: []byte("{}"),
			}},
			wantStatus:     http.StatusOK,
			wantSupported:  true,
			wantCategories: []string{"Movies", "TV"},
			wantDecrypted:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			crypt := &recordingCryptor{}
			h := &Clients{Clients: tt.store, Master: crypt, BaseURL: "http://t"}

			req := httptest.NewRequest(http.MethodGet, "/clients/"+cid.String()+"/categories", nil)
			req = req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
				&auth.Claims{UserID: uid.String()}))
			req = withURLParam(req, "id", cid.String())
			w := httptest.NewRecorder()
			h.Categories(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var resp struct {
				Supported  bool     `json:"supported"`
				Categories []string `json:"categories"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if resp.Supported != tt.wantSupported {
				t.Errorf("supported = %v, want %v", resp.Supported, tt.wantSupported)
			}
			if !reflect.DeepEqual(resp.Categories, tt.wantCategories) {
				t.Errorf("categories = %v, want %v", resp.Categories, tt.wantCategories)
			}
			if crypt.decrypted != tt.wantDecrypted {
				t.Errorf("decrypted = %v, want %v (an unsupported client must never decrypt its config)",
					crypt.decrypted, tt.wantDecrypted)
			}
		})
	}
}
