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

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
)

type fakeSonarrSettings struct {
	cfg      domain.SonarrConfig
	upserted *domain.SonarrConfig
}

func (f *fakeSonarrSettings) GetSonarr(context.Context, *crypto.MasterKey) (*domain.SonarrConfig, error) {
	c := f.cfg
	return &c, nil
}
func (f *fakeSonarrSettings) UpsertSonarr(_ context.Context, _ *crypto.MasterKey, c *domain.SonarrConfig) error {
	f.upserted = c
	f.cfg = *c
	return nil
}

func adminCtx(req *http.Request, uid uuid.UUID) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: uid.String(), Role: "admin"}))
}

func TestSonarr_Get_NeverLeaksKey(t *testing.T) {
	h := &Sonarr{
		Settings: &fakeSonarrSettings{cfg: domain.SonarrConfig{Enabled: true, URL: "http://s:8989", APIKey: "topsecret"}},
		BaseURL:  "http://t", Timeout: time.Second,
	}
	w := httptest.NewRecorder()
	h.Get(w, httptest.NewRequest(http.MethodGet, "/system/sonarr", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "topsecret") {
		t.Fatalf("response leaked the API key: %s", body)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if v["api_key_set"] != true {
		t.Errorf("api_key_set = %v, want true", v["api_key_set"])
	}
	if _, present := v["api_key"]; present {
		t.Errorf("response must not contain an api_key field")
	}
}

func TestSonarr_Update_EmptyKeyPreservedAndOwnerSet(t *testing.T) {
	store := &fakeSonarrSettings{cfg: domain.SonarrConfig{APIKey: "existing"}}
	h := &Sonarr{Settings: store, BaseURL: "http://t", Timeout: time.Second}

	body := `{"enabled":true,"sonarr_url":"http://sonarr:8989","api_key":"","poll_interval_sec":900}`
	req := adminCtx(httptest.NewRequest(http.MethodPut, "/system/sonarr", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if store.upserted == nil {
		t.Fatal("upsert not called")
	}
	if store.upserted.APIKey != "" {
		t.Errorf("empty api_key must pass through as empty (repo preserves stored), got %q", store.upserted.APIKey)
	}
	if store.upserted.OwnerUserID == nil {
		t.Errorf("owner must be set to the saving admin")
	}
}

func TestSonarr_Update_RejectsEnabledWithoutURL(t *testing.T) {
	h := &Sonarr{Settings: &fakeSonarrSettings{}, BaseURL: "http://t", Timeout: time.Second}
	body := `{"enabled":true,"sonarr_url":""}`
	req := adminCtx(httptest.NewRequest(http.MethodPut, "/system/sonarr", strings.NewReader(body)), uuid.New())
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestSonarr_Test_UsesStoredConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"4.0.0","appName":"Sonarr"}`))
	}))
	defer srv.Close()

	h := &Sonarr{
		Settings: &fakeSonarrSettings{cfg: domain.SonarrConfig{URL: srv.URL, APIKey: "k"}},
		BaseURL:  "http://t", Timeout: 3 * time.Second,
	}
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/system/sonarr/test", strings.NewReader(`{}`)), uuid.New())
	w := httptest.NewRecorder()
	h.Test(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v["ok"] != true || v["version"] != "4.0.0" {
		t.Errorf("unexpected test result: %v", v)
	}
}
