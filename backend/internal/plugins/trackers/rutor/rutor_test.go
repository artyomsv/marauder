package rutor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://rutor.info/torrent/12345/the.movie":     true,
		"https://www.rutor.info/torrent/12345/the.movie": true,
		"https://new-rutor.org/torrent/12345/the.movie/": true,
		// rutor.org is a retired fetch target but stayed in parseDomains, so
		// topics stored against it before 2026-09-03 must still parse.
		"https://rutor.org/torrent/12345/the.movie": true,
		"https://rutor.info/search/movie":           false,
		"":                                          false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Custom: []string{"rutor.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{}
	if !p.CanParse("https://rutor.example/torrent/123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/torrent/123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://rutor.org/torrent/99999/the.movie")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != "99999" {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "new-rutor.org"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "new-rutor.org" {
		t.Errorf("effectiveDomain = %q, want new-rutor.org", got)
	}
	// A test-injected p.domain (non-empty, ≠ defaultDomain) must win over
	// the resolver — this is what keeps every httptest-based test working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

// TestEffectiveDomain_UnsetFallsBackToDefault guards the rutor-specific
// wrinkle: unlike kinozal, a plugin literal built without setting the
// domain field at all (as the pre-existing e2e test does) must resolve to
// the compiled default, not an empty host.
func TestEffectiveDomain_UnsetFallsBackToDefault(t *testing.T) {
	p := &plugin{}
	if got := p.effectiveDomain(); got != defaultDomain {
		t.Errorf("effectiveDomain with unset domain = %q, want %q", got, defaultDomain)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"rutor.info", "new-rutor.org"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is
// hit. Mirrors the kinozal/nnmclub test helper of the same purpose.
type hostRecordingRewrite struct {
	target string
	hosts  []string
}

func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	req.URL.Scheme = "http"
	req.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(req)
}

// fixtureRutorHTML mirrors the shape of a real topic page as observed on
// 2026-09-03: the mirror brands the <title> with its own host as a prefix,
// the site chrome carries images of its own, and the poster sits as the
// first image inside <table id="details">.
const fixtureRutorHTML = `<html><head><title>rutor.info :: The Movie [1080p]</title></head>
<body>
<img src="//cdnbunny.org/logo.jpg" alt="rutor.info logo" />
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Movie">magnet</a>
<table id="details"><tr><td></td><td><br /><img src="https://i128.fastpic.org/big/2026/0902/a4/poster.jpg" /><br />
<b>Название: </b>The Movie</td></tr></table>
</body></html>`

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — rutor has no id-based
// rebuild (unlike kinozal), so canonicalURL is the only place this
// override actually takes effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureRutorHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{httpClient: &http.Client{Transport: rec}}

	topic := &domain.Topic{URL: "https://rutor.org/torrent/12345/the.movie"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(check.DisplayName, "The Movie") {
		t.Errorf("display name = %q, want it to contain The Movie", check.DisplayName)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "new-rutor.org" {
		t.Errorf("fetch host = %q, want active domain new-rutor.org", rec.hosts[0])
	}
}

// TestDownload_RewritesToActiveDomain mirrors TestCheck_RewritesToActiveDomain
// for Download: during a primary-domain outage with an admin-configured
// active mirror, Download must fetch the mirror host too.
func TestDownload_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureRutorHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{httpClient: &http.Client{Transport: rec}}

	topic := &domain.Topic{URL: "https://rutor.org/torrent/12345/the.movie"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.Contains(strings.ToLower(payload.MagnetURI), "urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("magnet missing infohash: %q", payload.MagnetURI)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "new-rutor.org" {
		t.Errorf("fetch host = %q, want active domain new-rutor.org", rec.hosts[0])
	}
}

// TestFetch_RejectsOffSiteHost closes rutor's previously-missing host guard:
// fetch must refuse any URL that is not a known/admin-configured rutor host,
// before a request is ever dialed — so no test server is needed here.
func TestFetch_RejectsOffSiteHost(t *testing.T) {
	p := &plugin{httpClient: &http.Client{}}
	bad := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:6379/",
		"https://evil.com/torrent/1",
		"https://rutor.info.evil.com/torrent/1",
		"ftp://rutor.info/torrent/1",
		"://malformed",
		// The download-host allowance strips one "d." prefix and re-checks
		// the base, so it must not become a way in for an unrelated host.
		"https://d.evil.com/download/1",
		"https://d.rutor.info.evil.com/download/1",
		"https://d.d.evil.com/download/1",
		// rutor.org is parseable but retired as a fetch target: it is a
		// stale clone that 404s every current id, so a request must never
		// be built against it.
		"https://rutor.org/torrent/1",
		"https://d.rutor.org/download/1",
	}
	for _, target := range bad {
		if _, err := p.fetch(context.Background(), target); err == nil {
			t.Errorf("fetch(%q) should be refused by the SSRF guard", target)
		}
	}
}

// TestFetchHostAllowed_DownloadSubdomain locks the one host shape the guard
// must let through beyond the mirrors themselves: the .torrent host.
func TestFetchHostAllowed_DownloadSubdomain(t *testing.T) {
	for _, host := range []string{"rutor.info", "new-rutor.org", "d.rutor.info", "d.new-rutor.org"} {
		if !fetchHostAllowed(host) {
			t.Errorf("fetchHostAllowed(%q) = false, want true", host)
		}
	}
}

func TestCleanTitle_StripsMirrorBranding(t *testing.T) {
	cases := map[string]string{
		"rutor.info :: Ирек Гильмутдинов - Привет магия!": "Ирек Гильмутдинов - Привет магия!",
		"new-rutor.org :: Мастер Рун. Книга 9 (2026) MP3": "Мастер Рун. Книга 9 (2026) MP3",
		"  rutor.info  ::  Padded  ":                      "Padded",
		// No host-shaped prefix: leave the title exactly as it is.
		"Plain Release Name": "Plain Release Name",
		// A release name that itself contains " :: " must survive intact
		// once the mirror prefix (if any) is gone.
		"rutor.info :: Show :: Season 2": "Show :: Season 2",
		"Show :: Season 2":               "Show :: Season 2",
	}
	for in, want := range cases {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractImageURL_PrefersDetailsTablePoster(t *testing.T) {
	// The site logo appears before the details table; the poster must win.
	if got := extractImageURL([]byte(fixtureRutorHTML), "rutor.info"); got != "https://i128.fastpic.org/big/2026/0902/a4/poster.jpg" {
		t.Errorf("ImageURL = %q, want the details-table poster", got)
	}
	// Protocol-relative and site-relative sources are absolutised.
	rel := `<table id="details"><tr><td><img src="//img.example/p.jpg" /></td></tr></table>`
	if got := extractImageURL([]byte(rel), "rutor.info"); got != "https://img.example/p.jpg" {
		t.Errorf("protocol-relative ImageURL = %q", got)
	}
	root := `<table id="details"><tr><td><img src="/pic/p.jpg" /></td></tr></table>`
	if got := extractImageURL([]byte(root), "rutor.info"); got != "https://rutor.info/pic/p.jpg" {
		t.Errorf("site-relative ImageURL = %q", got)
	}
	// A release with no poster reports none rather than the site chrome.
	none := `<img src="//cdnbunny.org/logo.jpg"><table id="details"><tr><td>no image</td></tr></table>`
	if got := extractImageURL([]byte(none), "rutor.info"); got != "" {
		t.Errorf("ImageURL with no poster = %q, want empty", got)
	}
}

func TestResolveMetadata_TitleAndPoster(t *testing.T) {
	p, _ := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureRutorHTML))
	})
	meta, err := p.ResolveMetadata(context.Background(), "https://rutor.info/torrent/12345/the.movie", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if meta.Title != "The Movie [1080p]" {
		t.Errorf("Title = %q, want the mirror prefix stripped", meta.Title)
	}
	if meta.ImageURL != "https://i128.fastpic.org/big/2026/0902/a4/poster.jpg" {
		t.Errorf("ImageURL = %q", meta.ImageURL)
	}
}

// realTorrent is a minimal single-file torrent whose info dict hashes to
// fixtureInfohash. Built by hand so the test never needs a network fetch.
const realTorrent = "d8:announce9:udp://x:14:infod6:lengthi1e4:name1:a" +
	"12:piece lengthi16384e6:pieces0:ee"

func torrentInfohash(t *testing.T) string {
	t.Helper()
	h, err := infohash.FromTorrent([]byte(realTorrent))
	if err != nil {
		t.Fatalf("fixture torrent is not parseable: %v", err)
	}
	return h
}

// downloadServer serves a topic page whose magnet carries wantHash, and a
// /download/<id> endpoint serving torrentBody.
func downloadServer(t *testing.T, magnetHash string, torrentStatus int, torrentBody string) (*plugin, *[]string) {
	t.Helper()
	var paths []string
	page := fmt.Sprintf(
		`<html><head><title>rutor.info :: A Release</title></head><body>`+
			`<a href="magnet:?xt=urn:btih:%s&amp;dn=A.Release">m</a></body></html>`, magnetHash)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasPrefix(r.URL.Path, "/download/") {
			if torrentStatus != 200 {
				w.WriteHeader(torrentStatus)
				return
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write([]byte(torrentBody))
			return
		}
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	p := &plugin{httpClient: &http.Client{
		Transport: &e2eHostRewrite{to: strings.TrimPrefix(srv.URL, "http://")},
	}}
	return p, &paths
}

// TestDownload_PrefersRealTorrentFile is the point of the .torrent upgrade:
// a magnet needs DHT/PEX peers to resolve metadata, the file does not.
func TestDownload_PrefersRealTorrentFile(t *testing.T) {
	hash := torrentInfohash(t)
	p, paths := downloadServer(t, hash, 200, realTorrent)

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatal("TorrentFile is empty, want the real .torrent bytes")
	}
	// qBittorrent's Add prefers MagnetURI whenever it is set, so returning
	// both would silently throw the file away.
	if payload.MagnetURI != "" {
		t.Errorf("MagnetURI = %q, want empty when a .torrent was fetched", payload.MagnetURI)
	}
	if payload.FileName != "rutor-12345.torrent" {
		t.Errorf("FileName = %q", payload.FileName)
	}
	if !contains(*paths, "/download/12345") {
		t.Errorf("download endpoint was not requested; paths=%v", *paths)
	}
}

func TestDownload_FallsBackToMagnetWhenTorrentHostFails(t *testing.T) {
	hash := torrentInfohash(t)
	p, _ := downloadServer(t, hash, http.StatusNotFound, "")

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("TorrentFile should be empty when the download host 404s")
	}
	if !strings.Contains(payload.MagnetURI, hash) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

// TestDownload_FallsBackWhenDownloadHostServesHTML covers the observed
// new-rutor.org behaviour: its proxied download link answers 200 with an
// HTML page, which must never be handed to a torrent client as a file.
func TestDownload_FallsBackWhenDownloadHostServesHTML(t *testing.T) {
	hash := torrentInfohash(t)
	p, _ := downloadServer(t, hash, 200, "<html><body>not a torrent</body></html>")

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("an HTML body must not be returned as TorrentFile")
	}
	if !strings.Contains(payload.MagnetURI, hash) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

// TestDownload_RejectsMismatchedTorrent guards the delivery bookkeeping:
// Check derives the topic hash from the page magnet, so a file with a
// different infohash would record a delivery that never matches the check.
func TestDownload_RejectsMismatchedTorrent(t *testing.T) {
	other := "0123456789abcdef0123456789abcdef01234567"
	p, _ := downloadServer(t, other, 200, realTorrent)

	topic := &domain.Topic{URL: "https://rutor.info/torrent/12345/a-release"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Error("a torrent whose infohash differs from the magnet must be rejected")
	}
	if !strings.Contains(payload.MagnetURI, other) {
		t.Errorf("MagnetURI = %q, want the page magnet", payload.MagnetURI)
	}
}

func TestTorrentURLs_ActiveThenCanonical(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "rutor" {
			return registry.DomainConfig{Active: "new-rutor.org"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	p := &plugin{}
	want := []string{
		"https://d.new-rutor.org/download/42",
		"https://d.rutor.info/download/42",
	}
	if got := p.torrentURLs("42"); !reflect.DeepEqual(got, want) {
		t.Errorf("torrentURLs = %v, want %v", got, want)
	}

	// On the canonical domain there is nothing to fall back to.
	registry.SetDomainResolver(nil)
	if got := p.torrentURLs("42"); !reflect.DeepEqual(got, []string{"https://d.rutor.info/download/42"}) {
		t.Errorf("torrentURLs on canonical domain = %v", got)
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

const searchFixtureHTML = `<html><body><table>
<tr><td>Header row without link</td></tr>
<tr class="gai"><td>22&nbsp;&#1048;&#1102;&#1083;&nbsp;26</td><td>
<a class="downgif" href="/download/975045"><img src="/s/i/d.gif" alt="D"></a>
<a href="magnet:?xt=urn:btih:aaaabbbbccccddddeeeeffff0000111122223333"><img src="/s/i/m.gif" alt="M"></a>
<a href="/torrent/975045/test-release-1080p">Test release <b>1080p</b></a></td>
<td align="right">1.4&nbsp;GB</td>
<td align="center"><span class="green">&nbsp;17&nbsp;</span>&nbsp;<span class="red">3</span></td></tr>
<tr class="tum"><td>21&nbsp;&#1048;&#1102;&#1083;&nbsp;26</td><td>
<a class="downgif" href="/download/975001"><img src="/s/i/d.gif" alt="D"></a>
<a href="/torrent/975001/another-release">Another release</a></td>
<td align="right">804.9&nbsp;MB</td>
<td align="center"><span class="green">2</span></td></tr>
</table></body></html>`

func newSearchTestPlugin(t *testing.T, handler http.HandlerFunc) (*plugin, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := &plugin{httpClient: &http.Client{
		Transport: &e2eHostRewrite{to: strings.TrimPrefix(srv.URL, "http://")},
	}}
	return p, srv
}

// e2eHostRewrite forces rutor.org -> test server (scheme https -> http).
type e2eHostRewrite struct{ to string }

func (h *e2eHostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = h.to
	return http.DefaultTransport.RoundTrip(req)
}

func TestSearch_ParsesResultsFromFixture(t *testing.T) {
	var gotPath string
	p, _ := newSearchTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(searchFixtureHTML))
	})
	results, err := p.Search(context.Background(), "test query", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/search/0/0/000/0/test%20query" {
		t.Errorf("request path = %q, want /search/0/0/000/0/test%%20query", gotPath)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0]
	if first.Title != "Test release 1080p" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://rutor.info/torrent/975045" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Size != "1.4 GB" {
		t.Errorf("Size = %q", first.Size)
	}
	if first.Seeders != 17 {
		t.Errorf("Seeders = %d", first.Seeders)
	}
	if results[1].Seeders != 2 {
		t.Errorf("second Seeders = %d", results[1].Seeders)
	}
}

func TestSearch_EmptyQuery_NoRequest(t *testing.T) {
	called := false
	p, _ := newSearchTestPlugin(t, func(http.ResponseWriter, *http.Request) { called = true })
	results, err := p.Search(context.Background(), "   ", nil)
	if err != nil || results != nil {
		t.Fatalf("empty query: results=%v err=%v, want nil,nil", results, err)
	}
	if called {
		t.Error("empty query must not hit the tracker")
	}
}

func TestSearch_NoRows_EmptyNotError(t *testing.T) {
	p, _ := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>no matches</body></html>`))
	})
	results, err := p.Search(context.Background(), "nothing", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestSearch_FetchError_Propagates(t *testing.T) {
	p, _ := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := p.Search(context.Background(), "anything", nil); err == nil {
		t.Fatal("Search on a 503 must return an error")
	}
}

func TestSearch_CapsAtFiftyResults(t *testing.T) {
	var rows strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&rows,
			`<tr class="gai"><td>d</td><td><a href="/torrent/%d/slug">Release %d</a></td>`+
				`<td align="right">1 GB</td><td align="center"><span class="green">1</span></td></tr>`, i, i)
	}
	page := "<html><body><table>" + rows.String() + "</table></body></html>"
	p, _ := newSearchTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	})
	results, err := p.Search(context.Background(), "many", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 50 {
		t.Errorf("results = %d, want capped at 50", len(results))
	}
}
