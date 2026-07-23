package kinozal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// searchFixtureHTML reproduces live kinozal.me browse.php markup (captured
// 2026-07-23): mixed attribute quoting (class="nam" / class='s' /
// class=bg), unclosed nam cells, and multiple class='s' cells per row of
// which only one is the size.
const searchFixtureHTML = `<html><body><table>
<tr class="mn"><td>header</td></tr>
<tr class='first bg'><td class="bt"><img src="/pic/cat/25.gif"></td><td class="nam"><a href="/details.php?id=2147332" class="r0">Планета Дюна / Planet Dune / 2021 / ПМ, СТ / WEB-DL (1080p)</a><td class='s'>2</td> <td class='s'>2.54 ГБ</td> <td class='sl_s'>5</td> <td class='sl_p'>0</td> <td class='s'>15.07.2026 в 15:55</td></tr>
<tr class=bg><td class="bt"><img src="/pic/cat/46.gif"></td><td class="nam"><a href="/details.php?id=2063569" class="r1">Дюна: Пророчество (1 сезон: 1-6 серии из 6) / Dune: Prophecy / 2024 / ДБ / WEB-DL (1080p)</a><td class='s'>14</td> <td class='s'>11.4 ГБ</td> <td class='sl_s'>29</td> <td class='sl_p'>1</td> <td class='s'>10.01.2025 в 09:12</td></tr>
</table></body></html>`

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

func writeCP1251Search(t *testing.T, w http.ResponseWriter, utf8HTML string) {
	t.Helper()
	enc, err := charmap.Windows1251.NewEncoder().String(utf8HTML)
	if err != nil {
		t.Fatalf("encode fixture to cp1251: %v", err)
	}
	_, _ = w.Write([]byte(enc))
}

func TestSearch_ParsesLiveShapedRows(t *testing.T) {
	var gotRawQuery string
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		writeCP1251Search(t, w, searchFixtureHTML)
	})
	results, err := p.Search(context.Background(), "Дюна", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Д=0xC4 ю=0xFE н=0xED а=0xE0 — the query must go out cp1251-encoded.
	if gotRawQuery != "s=%C4%FE%ED%E0" {
		t.Errorf("raw query = %q, want s=%%C4%%FE%%ED%%E0", gotRawQuery)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0]
	if first.Title != "Планета Дюна / Planet Dune / 2021 / ПМ, СТ / WEB-DL (1080p)" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://kinozal.tv/details.php?id=2147332" {
		t.Errorf("URL = %q", first.URL)
	}
	// The size is the s-cell that looks like a size — not the comments
	// count ("2") and not the date cell.
	if first.Size != "2.54 ГБ" {
		t.Errorf("Size = %q", first.Size)
	}
	if first.Seeders != 5 {
		t.Errorf("Seeders = %d", first.Seeders)
	}
	if results[1].Seeders != 29 || results[1].Size != "11.4 ГБ" {
		t.Errorf("second result = %+v", results[1])
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

func TestSearch_NoResultRows_EmptyNotError(t *testing.T) {
	p := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCP1251Search(t, w, `<html><body>Ничего не найдено</body></html>`)
	})
	results, err := p.Search(context.Background(), "nothing", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}
