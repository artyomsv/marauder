package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// newCategoriesServer stands in for qBittorrent's /api/v2/torrents/categories
// endpoint, serving the supplied raw JSON body (or a 500 when status != 200).
func newCategoriesServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/categories", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCategories_ReturnsSortedNames(t *testing.T) {
	srv := newCategoriesServer(t, http.StatusOK, `{
		"TV":     {"name":"TV","savePath":"/downloads/tv"},
		"Movies": {"name":"Movies","savePath":""},
		"TV/Anime": {"name":"TV/Anime","savePath":"/downloads/tv/anime"}
	}`)
	raw, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	p := &plugin{sessions: map[string]*session{}}

	got, err := p.Categories(context.Background(), raw)
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	want := []string{"Movies", "TV", "TV/Anime"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("categories = %v, want %v", got, want)
	}
}

func TestCategories_Empty(t *testing.T) {
	srv := newCategoriesServer(t, http.StatusOK, `{}`)
	raw, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	p := &plugin{sessions: map[string]*session{}}

	got, err := p.Categories(context.Background(), raw)
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("categories = %v, want empty", got)
	}
}

func TestCategories_NonOKStatus_ReturnsError(t *testing.T) {
	srv := newCategoriesServer(t, http.StatusForbidden, "Forbidden")
	raw, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	p := &plugin{sessions: map[string]*session{}}

	if _, err := p.Categories(context.Background(), raw); err == nil {
		t.Fatal("expected error on non-200 categories response")
	}
}
