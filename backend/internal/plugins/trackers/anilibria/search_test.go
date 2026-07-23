package anilibria

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// searchFixtureJSON mirrors the live /app/search/releases response shape
// (verified 2026-07-23): a plain JSON array of releases.
const searchFixtureJSON = `[
	{"id":10290,"alias":"one-piece","name":{"main":"Ван-Пис"}},
	{"id":420,"alias":"","name":{"main":"Без алиаса — пропустить"}},
	{"id":777,"alias":"no-title","name":{"main":""}}
]`

func newSearchTestPlugin(t *testing.T, handler http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &plugin{
		httpClient:        srv.Client(),
		aniLibertyAPIBase: srv.URL,
	}
}

func TestSearch_MapsReleasesToCanonicalURLs(t *testing.T) {
	var gotPath, gotQuery string
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(searchFixtureJSON))
	})
	results, err := p.Search(context.Background(), "ван пис", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/app/search/releases" || gotQuery != "ван пис" {
		t.Errorf("request = %s?query=%s, want /app/search/releases?query=ван пис", gotPath, gotQuery)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (empty-alias entry skipped)", len(results))
	}
	if results[0].Title != "Ван-Пис" {
		t.Errorf("Title = %q", results[0].Title)
	}
	if results[0].URL != "https://aniliberty.top/anime/releases/release/one-piece/" {
		t.Errorf("URL = %q", results[0].URL)
	}
	if results[0].Seeders != -1 || results[0].Category != "Anime" {
		t.Errorf("meta = %+v", results[0])
	}
	// Missing name.main falls back to the alias, never an empty title.
	if results[1].Title != "no-title" {
		t.Errorf("fallback Title = %q", results[1].Title)
	}
	// The emitted URL must round-trip through the plugin's own CanParse.
	if !p.CanParse(results[0].URL) {
		t.Errorf("CanParse rejects emitted URL %q", results[0].URL)
	}
}

func TestSearch_EmptyQuery_NoRequest(t *testing.T) {
	called := false
	p := newSearchTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := p.Search(context.Background(), "  ", nil)
	if err != nil || results != nil {
		t.Fatalf("empty query: results=%v err=%v, want nil,nil", results, err)
	}
	if called {
		t.Error("empty query must not hit the API")
	}
}

func TestSearch_NonJSONResponse_Errors(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>cf challenge</html>"))
	})
	if _, err := p.Search(context.Background(), "anything", nil); err == nil {
		t.Fatal("non-JSON body must error, not silently return zero results")
	}
}
