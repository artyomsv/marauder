package freetorrents

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_KnownURLs_MatchExpected(t *testing.T) {
	p := New("", nil)
	cases := map[string]bool{
		"https://free-torrents.org/forum/viewtopic.php?t=12345":     true,
		"https://www.free-torrents.org/forum/viewtopic.php?t=12345": true,
		"https://free-torrents.org/forum/index.php":                 false,
		"": false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == "freetorrents" {
			return registry.DomainConfig{Custom: []string{"free-torrents.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := New("", nil)
	if !p.CanParse("https://free-torrents.example/forum/viewtopic.php?t=123") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/forum/viewtopic.php?t=123") {
		t.Error("unlisted domain must not parse")
	}
}

func TestParse_ExtractsTopicIDAfterHostShift(t *testing.T) {
	p := New("", nil)
	topic, err := p.Parse(context.Background(), "https://free-torrents.org/forum/viewtopic.php?t=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "free-torrents.mirror"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := New("", nil)
	if got := p.effectiveDomain(); got != "free-torrents.mirror" {
		t.Errorf("effectiveDomain = %q, want free-torrents.mirror", got)
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
	want := []string{"free-torrents.org"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}
