package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeSonarrInstances struct {
	list    []domain.SonarrInstance
	getByID *domain.SonarrInstance
	created *domain.SonarrInstance
	updated *domain.SonarrInstance
	deleted *uuid.UUID
}

func (f *fakeSonarrInstances) List(context.Context, *crypto.MasterKey) ([]domain.SonarrInstance, error) {
	return f.list, nil
}
func (f *fakeSonarrInstances) Get(_ context.Context, _ *crypto.MasterKey, id uuid.UUID) (*domain.SonarrInstance, error) {
	if f.getByID != nil {
		return f.getByID, nil
	}
	return nil, repo.ErrNotFound
}
func (f *fakeSonarrInstances) Create(_ context.Context, _ *crypto.MasterKey, inst domain.SonarrInstance) (*domain.SonarrInstance, error) {
	inst.ID = uuid.New()
	f.created = &inst
	return &inst, nil
}
func (f *fakeSonarrInstances) Update(_ context.Context, _ *crypto.MasterKey, id uuid.UUID, inst domain.SonarrInstance) (*domain.SonarrInstance, error) {
	inst.ID = id
	f.updated = &inst
	return &inst, nil
}
func (f *fakeSonarrInstances) Delete(_ context.Context, id uuid.UUID) error {
	f.deleted = &id
	return nil
}

func adminCtx(req *http.Request, uid uuid.UUID) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: uid.String(), Role: "admin"}))
}

func TestSonarr_List_NeverLeaksKey(t *testing.T) {
	h := &Sonarr{
		Instances: &fakeSonarrInstances{list: []domain.SonarrInstance{
			{ID: uuid.New(), Name: "TV", Enabled: true, URL: "http://s:8989", APIKey: "topsecret"},
		}},
		BaseURL: "http://t", Timeout: time.Second,
	}
	w := httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/system/sonarr/instances", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "topsecret") {
		t.Fatalf("response leaked the API key: %s", body)
	}
	var v []map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 || v[0]["api_key_set"] != true {
		t.Errorf("api_key_set = %v, want true", v)
	}
	if _, present := v[0]["api_key"]; present {
		t.Errorf("response must not contain an api_key field")
	}
}

func TestSonarr_Create_SetsOwnerAndEmptyKeyPassthrough(t *testing.T) {
	store := &fakeSonarrInstances{}
	h := &Sonarr{Instances: store, BaseURL: "http://t", Timeout: time.Second}

	body := `{"name":"TV","enabled":true,"sonarr_url":"http://sonarr:8989","api_key":"","poll_interval_sec":900}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/instances", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if store.created == nil {
		t.Fatal("create not called")
	}
	if store.created.APIKey != "" {
		t.Errorf("empty api_key must pass through as empty, got %q", store.created.APIKey)
	}
	if store.created.OwnerUserID == nil {
		t.Errorf("owner must be set to the saving admin")
	}
	if store.created.Name != "TV" {
		t.Errorf("name not persisted: %q", store.created.Name)
	}
}

func TestSonarr_Create_RejectsEnabledWithoutURL(t *testing.T) {
	h := &Sonarr{Instances: &fakeSonarrInstances{}, BaseURL: "http://t", Timeout: time.Second}
	body := `{"name":"TV","enabled":true,"sonarr_url":""}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/instances", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestSonarr_Create_RejectsMissingName(t *testing.T) {
	h := &Sonarr{Instances: &fakeSonarrInstances{}, BaseURL: "http://t", Timeout: time.Second}
	body := `{"name":"","enabled":false,"sonarr_url":"http://s:8989"}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/instances", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422 for missing name, got %d", w.Code)
	}
}

func TestSonarr_Update_EmptyKeyPassthrough(t *testing.T) {
	store := &fakeSonarrInstances{}
	h := &Sonarr{Instances: store, BaseURL: "http://t", Timeout: time.Second}

	body := `{"name":"TV","enabled":true,"sonarr_url":"http://sonarr:8989","api_key":"","poll_interval_sec":900}`
	req := adminCtx(httptest.NewRequest(http.MethodPut, "/system/sonarr/instances/x", strings.NewReader(body)), uuid.New())
	req = withURLParam(req, "id", uuid.New().String())
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if store.updated == nil || store.updated.APIKey != "" {
		t.Errorf("empty api_key must pass through as empty (repo preserves stored)")
	}
}

func TestSonarr_Delete(t *testing.T) {
	store := &fakeSonarrInstances{}
	h := &Sonarr{Instances: store, BaseURL: "http://t", Timeout: time.Second}
	id := uuid.New()
	req := adminCtx(httptest.NewRequest(http.MethodDelete, "/system/sonarr/instances/x", nil), uuid.New())
	req = withURLParam(req, "id", id.String())
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if store.deleted == nil || *store.deleted != id {
		t.Errorf("delete not called with the right id")
	}
}

func TestSonarr_TestExisting_UsesStoredConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"4.0.0","appName":"Sonarr"}`))
	}))
	defer srv.Close()

	h := &Sonarr{
		Instances: &fakeSonarrInstances{getByID: &domain.SonarrInstance{
			ID: uuid.New(), Name: "TV", URL: srv.URL, APIKey: "k",
		}},
		BaseURL: "http://t", Timeout: 3 * time.Second,
	}
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/instances/x/test", strings.NewReader(`{}`)), uuid.New())
	req = withURLParam(req, "id", uuid.New().String())
	w := httptest.NewRecorder()
	h.TestExisting(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v["ok"] != true || v["version"] != "4.0.0" {
		t.Errorf("unexpected test result: %v", v)
	}
}

func TestSonarr_Test_ConnectionFailure_SanitizedError(t *testing.T) {
	// Port 1 is closed → the dial fails. The handler must return a generic
	// 422 and must NOT echo the raw connection error (which can leak the
	// resolved host/IP / "connection refused" details).
	h := &Sonarr{Log: zerolog.Nop(), BaseURL: "http://t", Timeout: time.Second}
	body := `{"sonarr_url":"http://127.0.0.1:1","api_key":"k"}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/test", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Test(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	respBody := strings.ToLower(w.Body.String())
	for _, leak := range []string{"127.0.0.1:1", "connection refused", "dial tcp"} {
		if strings.Contains(respBody, leak) {
			t.Errorf("response leaked raw connection detail %q: %s", leak, w.Body.String())
		}
	}
}
