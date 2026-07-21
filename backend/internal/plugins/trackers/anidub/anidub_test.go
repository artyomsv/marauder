package anidub

import (
	"context"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
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
