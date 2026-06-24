package sonarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_SystemStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/system/status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Api-Key") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"4.0.14","appName":"Sonarr","instanceName":"Sonarr"}`))
	}))
	defer srv.Close()

	// Trailing slash must be trimmed.
	st, err := NewClient(srv.URL+"/", "good", 5*time.Second).SystemStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Version != "4.0.14" || st.AppName != "Sonarr" {
		t.Errorf("unexpected status: %+v", st)
	}

	if _, err := NewClient(srv.URL, "bad", 5*time.Second).SystemStatus(context.Background()); err == nil {
		t.Errorf("want auth error for bad key")
	}
}

func TestClient_GrabHistorySince(t *testing.T) {
	t1 := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)

	var gotAPIKey, gotEventType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotEventType = r.URL.Query().Get("eventType")
		// Sonarr returns newest-first.
		page := historyPage{Page: 1, PageSize: 100, TotalRecords: 3, Records: []HistoryRecord{
			{ID: 3, Date: t3, EventType: "grabbed", Data: HistoryData{NzbInfoURL: "u3"}},
			{ID: 2, Date: t2, EventType: "grabbed", Data: HistoryData{NzbInfoURL: "u2"}},
			{ID: 1, Date: t1, EventType: "grabbed", Data: HistoryData{NzbInfoURL: "u1"}},
		}}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()

	recs, err := NewClient(srv.URL, "k", 5*time.Second).GrabHistorySince(context.Background(), t1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// t1 is not After(t1) so it's excluded; t2,t3 returned chronologically.
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if recs[0].ID != 2 || recs[1].ID != 3 {
		t.Errorf("want chronological [2,3], got [%d,%d]", recs[0].ID, recs[1].ID)
	}
	if gotAPIKey != "k" {
		t.Errorf("api key header = %q", gotAPIKey)
	}
	if gotEventType != "1" {
		t.Errorf("eventType filter = %q, want 1 (grabbed)", gotEventType)
	}
}
