package lostfilm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// searchFixtureJSON mirrors the live /ajaxik.php response shape (verified
// 2026-07-23): data.series is what matters; data.names (actors) must be
// ignored.
const searchFixtureJSON = `{"data":{"series":[` +
	`{"id":"1146","icon":"/Static/Images/1146/Posters/icon.jpg","title":"Лаки","title_orig":"Lucky","link":"/series/Lucky"},` +
	`{"id":"999","icon":"/i.jpg","title":"Одинаковое","title_orig":"Одинаковое","link":"/series/Same_Title"},` +
	`{"id":"666","icon":"/i.jpg","title":"Мимо","title_orig":"Off-site","link":"/persons/Not_A_Series"}` +
	`],"names":[{"id":"57135","title":"Лаки Джонсон","title_orig":"Lucky Johnson","link":"/persons/Lucky_Johnson"}]}}`

func newSearchTestPlugin(t *testing.T, handler http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:  forumcommon.New(),
		domain:    "www.lostfilm.tv",
		transport: &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host},
	}
}

func TestSearch_ParsesSeriesEntries(t *testing.T) {
	var gotQuery url.Values
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(searchFixtureJSON))
	})
	results, err := p.Search(context.Background(), "Лаки", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery.Get("act") != "common" || gotQuery.Get("type") != "search" || gotQuery.Get("val") != "Лаки" {
		t.Errorf("query = %v, want act=common type=search val=Лаки", gotQuery)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (persons link filtered out)", len(results))
	}
	if results[0].Title != "Лаки / Lucky" {
		t.Errorf("Title = %q, want combined ru/orig", results[0].Title)
	}
	if results[0].URL != "https://www.lostfilm.tv/series/Lucky/" {
		t.Errorf("URL = %q", results[0].URL)
	}
	if results[0].Seeders != -1 || results[0].Category != "Series" {
		t.Errorf("result meta = %+v", results[0])
	}
	// Identical ru/orig titles must not duplicate ("X / X").
	if results[1].Title != "Одинаковое" {
		t.Errorf("second Title = %q, want no duplicated orig", results[1].Title)
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
		t.Error("empty query must not hit the tracker")
	}
}

func TestSearch_NonJSONResponse_Errors(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	})
	if _, err := p.Search(context.Background(), "anything", nil); err == nil {
		t.Fatal("non-JSON body must error, not silently return zero results")
	}
}

// TestSearch_NoMatches_IsEmptyNotAnError pins the shape LostFilm returns
// when nothing matches. The API changes the TYPE of `data` with the result
// count: {"data":{"series":[…]}} when it matches, {"data":[],"result":"ok"}
// when it does not, because PHP encodes an empty associative array as a JSON
// array. Decoding straight into the object form made every zero-result query
// report "search failed" — the user was told the tracker broke when in fact
// their query simply matched nothing. Body captured from the live API
// 2026-09-03.
func TestSearch_NoMatches_IsEmptyNotAnError(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"result":"ok"}`))
	})
	results, err := p.Search(context.Background(), "no such series", nil)
	if err != nil {
		t.Fatalf("a zero-result search must not be an error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

// TestSearch_MalformedDataObject_StillErrors: tolerating the array form must
// not turn every decode problem into a silent empty result.
func TestSearch_MalformedDataObject_StillErrors(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"series":"not-an-array"}}`))
	})
	if _, err := p.Search(context.Background(), "x", nil); err == nil {
		t.Fatal("a malformed data object must still be an error")
	}
}

// TestSearch_UnexpectedDataShape_Errors: only the object and array forms are
// LostFilm's. Anything else is the endpoint having changed or errored, and
// treating it as "nothing matched" would report a broken search as an empty
// one — the same class of lie the array tolerance was added to remove.
func TestSearch_UnexpectedDataShape_Errors(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"string", `{"data":"unauthorized"}`},
		{"number", `{"data":0}`},
		{"null", `{"data":null}`},
		{"key absent", `{"result":"ok"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			})
			if _, err := p.Search(context.Background(), "x", nil); err == nil {
				t.Fatal("an unexpected data shape must be an error, not an empty result")
			}
		})
	}
}
