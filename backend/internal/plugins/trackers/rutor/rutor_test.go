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
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://rutor.org/torrent/12345/the.movie":     true,
		"https://www.rutor.org/torrent/12345/the.movie": true,
		"https://rutor.info/torrent/12345/the.movie":    true,
		"https://rutor.org/search/movie":                false,
		"":                                              false,
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
		return registry.DomainConfig{Active: "rutor.info"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "rutor.info" {
		t.Errorf("effectiveDomain = %q, want rutor.info", got)
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
	want := []string{"rutor.org", "rutor.info"}
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

const fixtureRutorHTML = `<html><head><title>The Movie [1080p] :: Rutor.org</title></head>
<body>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Movie">magnet</a>
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
			return registry.DomainConfig{Active: "rutor.info"}
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
	if rec.hosts[0] != "rutor.info" {
		t.Errorf("fetch host = %q, want active domain rutor.info", rec.hosts[0])
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
			return registry.DomainConfig{Active: "rutor.info"}
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
	if rec.hosts[0] != "rutor.info" {
		t.Errorf("fetch host = %q, want active domain rutor.info", rec.hosts[0])
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
		"https://rutor.org.evil.com/torrent/1",
		"ftp://rutor.org/torrent/1",
		"://malformed",
	}
	for _, target := range bad {
		if _, err := p.fetch(context.Background(), target); err == nil {
			t.Errorf("fetch(%q) should be refused by the SSRF guard", target)
		}
	}
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
	if first.URL != "https://rutor.org/torrent/975045" {
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
