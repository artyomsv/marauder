package rutracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// searchFixtureHTML is a representative tracker.php result page (selectors
// verified against live markup 2026-07-23): an authenticated shell with two
// tCenter result rows. Encoded to cp1251 by the test server to exercise the
// decode path.
const searchFixtureHTML = `<html><body><span id="logged-in-username">user</span>
<table id="tor-tbl">
<tr id="trs-tr-100" class="tCenter hl-tr" data-topic_id="100">
<td class="row1 t-ico"></td>
<td class="row1 f-name-col"><div class="f-name"><a class="gen f ts-text" href="tracker.php?f=313">Зарубежное кино</a></div></td>
<td class="row4 med tLeft t-title-col"><div class="t-title"><a data-topic_id="100" class="med tLink tt-text ts-text hl-tags bold" href="viewtopic.php?t=100">Дюна <span class="brackets-pair">[2026]</span></a></div></td>
<td class="row1 t-author-col"></td>
<td class="row4 med tor-size" data-ts_text="1500000000"><a class="small tr-dl dl-stub" href="dl.php?t=100">1.4&nbsp;GB &darr;</a></td>
<td class="row4 nowrap" data-ts_text="17"><b class="seedmed">17</b></td>
<td class="row4 leechmed bold">3</td><td class="row4 small nowrap">22-Июл-26</td>
</tr>
<tr id="trs-tr-200" class="tCenter hl-tr" data-topic_id="200">
<td class="row1 t-ico"></td>
<td class="row1 f-name-col"><div class="f-name"><a class="gen f ts-text" href="tracker.php?f=7">Сериалы</a></div></td>
<td class="row4 med tLeft t-title-col"><div class="t-title"><a data-topic_id="200" class="med tLink tt-text ts-text hl-tags bold" href="viewtopic.php?t=200">Другой релиз</a></div></td>
<td class="row1 t-author-col"></td>
<td class="row4 med tor-size" data-ts_text="800000000"><a class="small tr-dl dl-stub" href="dl.php?t=200">804&nbsp;MB</a></td>
<td class="row4 nowrap" data-ts_text="2"><b class="seedmed">2</b></td>
<td class="row4 leechmed bold">1</td><td class="row4 small nowrap">21-Июл-26</td>
</tr>
</table></body></html>`

const searchLoginShellHTML = `<html><body><form id="login-form">login required</form></body></html>`

func newSearchTestPlugin(t *testing.T, handler http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &plugin{
		sessions: forumcommon.New(),
		domain:   defaultDomain,
		transport: &e2etest.HostRewriteTransport{
			From: defaultDomain,
			To:   strings.TrimPrefix(srv.URL, "http://"),
		},
	}
}

func testCreds() *domain.TrackerCredential {
	return &domain.TrackerCredential{UserID: uuid.New(), Username: "user"}
}

func writeCP1251(t *testing.T, w http.ResponseWriter, utf8HTML string) {
	t.Helper()
	enc, err := charmap.Windows1251.NewEncoder().String(utf8HTML)
	if err != nil {
		t.Fatalf("encode fixture to cp1251: %v", err)
	}
	_, _ = w.Write([]byte(enc))
}

func TestSearch_NilCreds_ErrSearchRequiresCredentials(t *testing.T) {
	called := false
	p := newSearchTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	_, err := p.Search(context.Background(), "anything", nil)
	if !errors.Is(err, registry.ErrSearchRequiresCredentials) {
		t.Fatalf("err = %v, want ErrSearchRequiresCredentials", err)
	}
	if called {
		t.Error("nil creds must not hit the tracker")
	}
}

func TestSearch_ParsesAuthenticatedResults(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		writeCP1251(t, w, searchFixtureHTML)
	})
	results, err := p.Search(context.Background(), "Дюна", testCreds())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0]
	if first.Title != "Дюна [2026]" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://rutracker.org/forum/viewtopic.php?t=100" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Size != "1.4 GB" {
		t.Errorf("Size = %q", first.Size)
	}
	if first.Seeders != 17 {
		t.Errorf("Seeders = %d", first.Seeders)
	}
	if first.Category != "Зарубежное кино" {
		t.Errorf("Category = %q", first.Category)
	}
	if results[1].Title != "Другой релиз" || results[1].Seeders != 2 {
		t.Errorf("second result = %+v", results[1])
	}
}

func TestSearch_CyrillicQuery_CP1251Encoded(t *testing.T) {
	var gotRawQuery string
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		writeCP1251(t, w, searchFixtureHTML)
	})
	if _, err := p.Search(context.Background(), "Дюна", testCreds()); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotRawQuery != "nm=%C4%FE%ED%E0" {
		t.Errorf("raw query = %q, want nm=%%C4%%FE%%ED%%E0", gotRawQuery)
	}
}

func TestSearch_AnonymousShell_ErrSearchRequiresCredentials(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searchLoginShellHTML))
	})
	_, err := p.Search(context.Background(), "query", testCreds())
	if !errors.Is(err, registry.ErrSearchRequiresCredentials) {
		t.Fatalf("err = %v, want ErrSearchRequiresCredentials", err)
	}
}

func TestSearch_EmptyQuery_NoRequestNoCredsCheck(t *testing.T) {
	called := false
	p := newSearchTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := p.Search(context.Background(), "   ", nil)
	if err != nil || results != nil {
		t.Fatalf("empty query: results=%v err=%v, want nil,nil", results, err)
	}
	if called {
		t.Error("empty query must not hit the tracker")
	}
}

func TestSearch_FetchError_Propagates(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := p.Search(context.Background(), "anything", testCreds()); err == nil {
		t.Fatal("Search on a 503 must return an error")
	}
}
