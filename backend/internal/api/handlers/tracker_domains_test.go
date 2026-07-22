package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeDomainsTracker implements registry.Tracker + registry.WithDomains.
type fakeDomainsTracker struct{ name string }

func (f *fakeDomainsTracker) Name() string        { return f.name }
func (f *fakeDomainsTracker) DisplayName() string { return "Fake Domains Tracker" }
func (f *fakeDomainsTracker) CanParse(rawURL string) bool {
	return false
}
func (f *fakeDomainsTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeDomainsTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeDomainsTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeDomainsTracker) Domains() []string {
	return []string{"fake-domains.example", "fake-domains.mirror"}
}

func init() {
	registry.RegisterTracker(&fakeDomainsTracker{name: "fake-domains-test"})
}

// fakeDomainsStore is the test double for domainsStore.
type fakeDomainsStore struct {
	active string
	custom []string

	setCalled bool
	setName   string
	setActive string
	setCustom []string
	setErr    error
}

func (f *fakeDomainsStore) Get(string) (string, []string) {
	return f.active, f.custom
}

func (f *fakeDomainsStore) Set(_ context.Context, name, active string, custom []string) error {
	f.setCalled = true
	f.setName = name
	f.setActive = active
	f.setCustom = custom
	return f.setErr
}

func TestTrackerDomains_List_OnePerWithDomainsTracker(t *testing.T) {
	store := &fakeDomainsStore{active: "fake-domains.mirror", custom: []string{"fake-domains.custom"}}
	h := &TrackerDomains{Store: store, BaseURL: "http://test"}

	req := httptest.NewRequest(http.MethodGet, "/system/trackers/domains", nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var views []trackerDomainsView
	if err := json.Unmarshal(w.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found *trackerDomainsView
	for i := range views {
		// Generic adapters registered by sibling test files (trackers_test.go,
		// etc.) must not appear — none of them implement WithDomains.
		if views[i].Name != "fake-domains-test" {
			t.Errorf("unexpected non-WithDomains tracker in response: %q", views[i].Name)
			continue
		}
		found = &views[i]
	}
	if found == nil {
		t.Fatalf("expected an entry for fake-domains-test, got %+v", views)
	}
	if found.DisplayName != "Fake Domains Tracker" {
		t.Errorf("display_name = %q", found.DisplayName)
	}
	if found.DefaultDomain != "fake-domains.example" {
		t.Errorf("default_domain = %q, want fake-domains.example", found.DefaultDomain)
	}
	if len(found.KnownDomains) != 2 || found.KnownDomains[0] != "fake-domains.example" {
		t.Errorf("known_domains = %v", found.KnownDomains)
	}
	if len(found.CustomDomains) != 1 || found.CustomDomains[0] != "fake-domains.custom" {
		t.Errorf("custom_domains = %v", found.CustomDomains)
	}
	if found.ActiveDomain != "fake-domains.mirror" {
		t.Errorf("active_domain = %q", found.ActiveDomain)
	}
}

func TestTrackerDomains_Update_ValidBody_SetsLowercased(t *testing.T) {
	store := &fakeDomainsStore{}
	h := &TrackerDomains{Store: store, BaseURL: "http://test"}

	body := `{"active_domain":"Fake-Domains.Example","custom_domains":["Custom.Example"]}`
	req := httptest.NewRequest(http.MethodPut, "/system/trackers/fake-domains-test/domains", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !store.setCalled {
		t.Fatal("Store.Set was not called")
	}
	if store.setName != "fake-domains-test" {
		t.Errorf("Set name = %q", store.setName)
	}
	if store.setActive != "fake-domains.example" {
		t.Errorf("Set active = %q, want lowercased", store.setActive)
	}
	if len(store.setCustom) != 1 || store.setCustom[0] != "custom.example" {
		t.Errorf("Set custom = %v, want lowercased", store.setCustom)
	}
}

func TestTrackerDomains_Update_ActiveNotInKnownOrCustom_422(t *testing.T) {
	store := &fakeDomainsStore{}
	h := &TrackerDomains{Store: store, BaseURL: "http://test"}

	body := `{"active_domain":"totally-unknown.example","custom_domains":[]}`
	req := httptest.NewRequest(http.MethodPut, "/system/trackers/fake-domains-test/domains", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	if store.setCalled {
		t.Error("Store.Set must not be called when validation fails")
	}
}

func TestTrackerDomains_Update_InvalidCustomHostname_422(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{"scheme", "https://x.y"},
		{"port", "x.y:8080"},
		{"path", "x.y/z"},
		{"ip literal", "1.2.3.4"},
		{"invalid label", "-bad-.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeDomainsStore{}
			h := &TrackerDomains{Store: store, BaseURL: "http://test"}

			bodyBytes, err := json.Marshal(trackerDomainsReq{
				ActiveDomain:  "",
				CustomDomains: []string{tt.host},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPut, "/system/trackers/fake-domains-test/domains", strings.NewReader(string(bodyBytes)))
			req = withURLParam(req, "name", "fake-domains-test")
			w := httptest.NewRecorder()
			h.Update(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
			}
			if store.setCalled {
				t.Error("Store.Set must not be called when a custom hostname is invalid")
			}
		})
	}
}

func TestTrackerDomains_Update_UnknownTracker_404(t *testing.T) {
	store := &fakeDomainsStore{}
	h := &TrackerDomains{Store: store, BaseURL: "http://test"}

	body := `{"active_domain":"","custom_domains":[]}`
	req := httptest.NewRequest(http.MethodPut, "/system/trackers/nonexistent-tracker/domains", strings.NewReader(body))
	req = withURLParam(req, "name", "nonexistent-tracker")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if store.setCalled {
		t.Error("Store.Set must not be called for an unknown tracker")
	}
}

func TestTrackerDomains_Update_EmptyActiveDomain_RevertsToDefault(t *testing.T) {
	store := &fakeDomainsStore{active: "fake-domains.mirror"}
	h := &TrackerDomains{Store: store, BaseURL: "http://test"}

	body := `{"active_domain":"","custom_domains":[]}`
	req := httptest.NewRequest(http.MethodPut, "/system/trackers/fake-domains-test/domains", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !store.setCalled {
		t.Fatal("Store.Set was not called")
	}
	if store.setActive != "" {
		t.Errorf("Set active = %q, want empty (revert to default)", store.setActive)
	}
}

func TestTrackerDomains_Test_InvalidDomain_422(t *testing.T) {
	h := &TrackerDomains{Store: &fakeDomainsStore{}, BaseURL: "http://test"}

	body := `{"domain":"https://bad.example"}`
	req := httptest.NewRequest(http.MethodPost, "/system/trackers/fake-domains-test/domains/test", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Test(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
}

func TestTrackerDomains_Test_ProbeSuccess_ReturnsOKTrue(t *testing.T) {
	h := &TrackerDomains{
		Store:   &fakeDomainsStore{},
		Probe:   func(context.Context, string) error { return nil },
		BaseURL: "http://test",
	}

	body := `{"domain":"reachable.example"}`
	req := httptest.NewRequest(http.MethodPost, "/system/trackers/fake-domains-test/domains/test", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Test(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["ok"] != true {
		t.Errorf("ok = %v, want true", v["ok"])
	}
}

func TestTrackerDomains_Test_ProbeFailure_Returns200WithOKFalse(t *testing.T) {
	h := &TrackerDomains{
		Store:   &fakeDomainsStore{},
		Probe:   func(context.Context, string) error { return errors.New("unreachable") },
		BaseURL: "http://test",
	}

	body := `{"domain":"unreachable.example"}`
	req := httptest.NewRequest(http.MethodPost, "/system/trackers/fake-domains-test/domains/test", strings.NewReader(body))
	req = withURLParam(req, "name", "fake-domains-test")
	w := httptest.NewRecorder()
	h.Test(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ok:false is not an HTTP error); body=%s", w.Code, w.Body.String())
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["ok"] != false {
		t.Errorf("ok = %v, want false", v["ok"])
	}
	if v["detail"] != "unreachable" {
		t.Errorf("detail = %v, want %q", v["detail"], "unreachable")
	}
}
