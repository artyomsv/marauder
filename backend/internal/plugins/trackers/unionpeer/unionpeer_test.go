package unionpeer

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	cases := map[string]bool{
		"https://unionpeer.org/forum/viewtopic.php?t=12345":     true,
		"https://www.unionpeer.org/forum/viewtopic.php?t=12345": true,
		"https://unionpeer.net/forum/viewtopic.php?t=12345":     true,
		"https://unionpeer.com/forum/viewtopic.php?t=12345":     true,
		"https://unionpeer.org/forum/index.php":                 false,
		"":                                                      false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "unionpeer" {
			return registry.DomainConfig{Custom: []string{"unionpeer.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if !p.CanParse("https://unionpeer.example/forum/viewtopic.php?t=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/forum/viewtopic.php?t=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := &plugin{domain: defaultDomain}
	topic, err := p.Parse(context.Background(), "https://unionpeer.org/forum/viewtopic.php?t=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "unionpeer.net"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "unionpeer.net" {
		t.Errorf("effectiveDomain = %q, want unionpeer.net", got)
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
	want := []string{"unionpeer.org", "unionpeer.net", "unionpeer.com"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}
