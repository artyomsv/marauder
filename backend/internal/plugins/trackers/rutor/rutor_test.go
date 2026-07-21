package rutor

import (
	"context"
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
