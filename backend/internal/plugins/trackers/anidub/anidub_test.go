package anidub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// TestCanParse_KnownHost_Matches is a regression assert: anidub's old regex
// was anchored to tr.anidub.com with no www-optional. The new host-agnostic
// pattern (gated by registry.DomainAllowed) must keep matching that exact
// URL shape.
func TestCanParse_KnownHost_Matches(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://tr.anidub.com/anime/2026/the-series.html") {
		t.Error("must still match the canonical tr.anidub.com URL shape")
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anidub" {
			return registry.DomainConfig{Custom: []string{"anidub.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://anidub.example/anime/2026/the-series.html") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/anime/2026/the-series.html") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsSlugAfterHostShift(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	topic, err := p.Parse(context.Background(), "https://tr.anidub.com/anime/2026/the-series.html")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["slug"] != "the-series" {
		t.Errorf("slug = %v, want the-series", topic.Extra["slug"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "anidub.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "anidub.mirror" {
		t.Errorf("effectiveDomain = %q, want anidub.mirror", got)
	}
	// A test-injected p.domain (≠ defaultDomain) must win over the resolver —
	// this is what keeps every httptest-based e2e test working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := &plugin{}
	want := []string{"tr.anidub.com"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
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

const fixtureAnidubHTML = `<html>
<body>
<h1>Аниме сериал [HDTVRip] [1080p]</h1>
<div data-hash="0123456789ABCDEF0123456789ABCDEF01234567">hash here</div>
</body></html>`

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — anidub has no id-based
// rebuild, so canonicalURL is the only place this override actually takes
// effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureAnidubHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "anidub" {
			return registry.DomainConfig{Active: "anidub.mirror"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}

	topic := &domain.Topic{URL: "https://tr.anidub.com/anime/2026/the-series.html"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "anidub.mirror" {
		t.Errorf("fetch host = %q, want active domain anidub.mirror", rec.hosts[0])
	}
}
