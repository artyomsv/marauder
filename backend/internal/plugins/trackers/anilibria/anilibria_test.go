package anilibria

import (
	"context"
	"reflect"
	"testing"

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
