package tapochek

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

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	cases := map[string]bool{
		"https://tapochek.net/viewtopic.php?t=12345":     true,
		"https://www.tapochek.net/viewtopic.php?t=12345": true,
		"https://tapochek.net/index.php":                 false,
		"":                                               false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "tapochek" {
			return registry.DomainConfig{Custom: []string{"tapochek.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://tapochek.example/viewtopic.php?t=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/viewtopic.php?t=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	topic, err := p.Parse(context.Background(), "https://tapochek.net/viewtopic.php?t=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "tapochek.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "tapochek.mirror" {
		t.Errorf("effectiveDomain = %q, want tapochek.mirror", got)
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
	want := []string{"tapochek.net"}
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

const fixtureTapochekHTML = `<html><head><title>Мультсериал :: Tapochek.net</title></head>
<body>
<div>Info hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
</body></html>`

// TestCheck_RewritesToActiveDomain asserts that when the admin has
// configured an active domain override, Check fetches that host instead of
// the mirror recorded in the stored topic URL — tapochek has no id-based
// rebuild, so canonicalURL is the only place this override actually takes
// effect.
func TestCheck_RewritesToActiveDomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(fixtureTapochekHTML))
	}))
	t.Cleanup(srv.Close)

	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "tapochek" {
			return registry.DomainConfig{Active: "tapochek.mirror"}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	rec := &hostRecordingRewrite{target: strings.TrimPrefix(srv.URL, "http://")}
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain, transport: rec}

	topic := &domain.Topic{URL: "https://tapochek.net/viewtopic.php?t=12345"}
	if _, err := p.Check(context.Background(), topic, nil); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	if rec.hosts[0] != "tapochek.mirror" {
		t.Errorf("fetch host = %q, want active domain tapochek.mirror", rec.hosts[0])
	}
}
