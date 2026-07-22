package anilibria

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
	p := &plugin{apiBase: apiBase}
	cases := map[string]bool{
		"https://anilibria.tv/release/example-slug.html":     true,
		"https://www.anilibria.tv/release/example-slug.html": true,
		"https://anilibria.tv/catalog":                       false,
		"":                                                   false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anilibria" {
			return registry.DomainConfig{Custom: []string{"anilibria.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{apiBase: apiBase}
	if !p.CanParse("https://anilibria.example/release/example-slug.html") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/release/example-slug.html") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsSlugAfterHostShift(t *testing.T) {
	p := &plugin{apiBase: apiBase}
	topic, err := p.Parse(context.Background(), "https://anilibria.tv/release/example-slug.html")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["slug"] != "example-slug" {
		t.Errorf("slug = %v, want example-slug", topic.Extra["slug"])
	}
}

func TestEffectiveAPIBase_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "anilibria.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{apiBase: apiBase}
	if got := p.effectiveAPIBase(); got != "https://api.anilibria.mirror/v3" {
		t.Errorf("effectiveAPIBase = %q, want https://api.anilibria.mirror/v3", got)
	}
	// A test-injected p.apiBase (≠ the package apiBase const) must win over
	// the resolver — this is what keeps every httptest-based test working.
	p.apiBase = "http://127.0.0.1:9999/v3"
	if got := p.effectiveAPIBase(); got != "http://127.0.0.1:9999/v3" {
		t.Errorf("effectiveAPIBase with test override = %q, want http://127.0.0.1:9999/v3", got)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"anilibria.tv"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

func TestEffectivePageHost_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "anilibria.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{}
	if got := p.effectivePageHost(); got != "anilibria.mirror" {
		t.Errorf("effectivePageHost = %q, want anilibria.mirror", got)
	}
}

func TestEffectivePageHost_NoResolver_FallsBackToDefault(t *testing.T) {
	p := &plugin{}
	if got := p.effectivePageHost(); got != "anilibria.tv" {
		t.Errorf("effectivePageHost = %q, want anilibria.tv", got)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is
// hit. Mirrors the nnmclub/rutor test helper of the same purpose.
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

// TestDownload_RelativeURLFallback_UsesActiveDomain covers the Download
// relative-URL fallback (issue #126 finding 2): when the legacy API
// returns a torrent URL with no scheme/host, Download used to hardcode
// "https://anilibria.tv" as the base — which would keep dialing a dead
// primary domain even after the admin configured a working mirror. It
// must now derive the page host from the active domain instead.
func TestDownload_RelativeURLFallback_UsesActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v3/title"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{
				"names": {"ru": "Аниме Сериал"},
				"torrents": {"list": [{"torrent_id": 101, "quality": {"string": "BDRip"}, "url": "/upload/torrents/101.torrent"}]}
			}`))
		case strings.HasPrefix(r.URL.Path, "/upload/torrents/"):
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anilibria" {
			return registry.DomainConfig{Active: "anilibria.mirror"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	// apiBase is left at the compiled default so effectiveAPIBase() applies
	// the resolver override too; hostRecordingRewrite redirects every
	// outgoing request to the test server unconditionally regardless of
	// the host in the URL, while still recording what that host was.
	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{httpClient: &http.Client{Transport: rec}, apiBase: apiBase}

	topic := &domain.Topic{
		URL:   "https://anilibria.tv/release/anime-series.html",
		Extra: map[string]any{"slug": "anime-series"},
	}
	if _, err := p.Download(context.Background(), topic, nil, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(rec.hosts) < 2 {
		t.Fatalf("expected at least 2 requests (title fetch + relative torrent fetch), got %d: %v", len(rec.hosts), rec.hosts)
	}
	if rec.hosts[1] != "anilibria.mirror" {
		t.Errorf("relative-URL fallback host = %q, want active domain anilibria.mirror", rec.hosts[1])
	}
}
