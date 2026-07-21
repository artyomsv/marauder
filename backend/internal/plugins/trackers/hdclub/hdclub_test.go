package hdclub

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
	p := New("", nil)
	cases := map[string]bool{
		"https://hdclub.org/details.php?id=12345":     true,
		"https://www.hdclub.org/details.php?id=12345": true,
		"https://hdclub.org/userlist.php":             false,
		"":                                            false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "hdclub" {
			return registry.DomainConfig{Custom: []string{"hdclub.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := New("", nil)
	if !p.CanParse("https://hdclub.example/details.php?id=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/details.php?id=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := New("", nil)
	topic, err := p.Parse(context.Background(), "https://hdclub.org/details.php?id=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "hdclub.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := New("", nil)
	if got := p.effectiveDomain(); got != "hdclub.mirror" {
		t.Errorf("effectiveDomain = %q, want hdclub.mirror", got)
	}
	// A test-injected Domain (≠ defaultDomain) must win over the resolver —
	// this is what keeps every httptest-based e2e test working.
	p.Domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with test override = %q, want 127.0.0.1:9999", got)
	}
}

func TestDomains_CanonicalFirst(t *testing.T) {
	p := New("", nil)
	want := []string{"hdclub.org"}
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

const fixtureHDClubHTML = `<html><head><title>HD Movie 4K :: HD-Club</title></head>
<body>
<div>Info hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
</body></html>`

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — hdclub's Check re-fetches
// the stored topic URL directly, so canonicalURL is the only place this
// override actually takes effect there.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureHDClubHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "hdclub" {
			return registry.DomainConfig{Active: "hdclub.mirror"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := New(defaultDomain, rec)

	topic := &domain.Topic{URL: "https://hdclub.org/details.php?id=12345"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "hdclub.mirror" {
		t.Errorf("fetch host = %q, want active domain hdclub.mirror", rec.hosts[0])
	}
}
