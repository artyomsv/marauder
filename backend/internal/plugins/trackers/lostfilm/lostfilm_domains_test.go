package lostfilm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == pluginName {
			return registry.DomainConfig{Custom: []string{"lostfilm.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{sessions: nil, domain: defaultDomain}
	if !p.CanParse("https://lostfilm.example/series/The_Boys/") {
		t.Error("custom domain should parse")
	}
	if p.CanParse("https://evil.example/series/The_Boys/") {
		t.Error("unlisted domain must not parse")
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "lostfilm.win"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "lostfilm.win" {
		t.Errorf("effectiveDomain = %q, want lostfilm.win", got)
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
	want := []string{"www.lostfilm.tv", "lostfilm.tv", "lostfilm.win", "lostfilm.run"}
	if !reflect.DeepEqual(p.Domains(), want) {
		t.Errorf("Domains() = %v, want %v", p.Domains(), want)
	}
}

// TestValidateRedirectURL_CustomDomainAllowlisted asserts that a custom
// domain added via the admin resolver clears the allowlist step of
// validateRedirectURL. "lostfilm.example" is a reserved (RFC 2606) domain
// that never resolves via DNS, so — mirroring the existing DNS-dependent
// TestValidateRedirectURL table — we can't assert overall success without a
// DNS mock; instead we assert the failure (if any) never comes from the
// allowlist check, only (possibly) from the unconditional DNS/private-IP
// check that must still run for a custom domain exactly as it does for a
// known one.
func TestValidateRedirectURL_CustomDomainAllowlisted(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == pluginName {
			return registry.DomainConfig{Custom: []string{"lostfilm.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })

	err := validateRedirectURL("https://lostfilm.example/td.php?s=x")
	if err != nil && strings.Contains(err.Error(), "not in the LostFilm allowlist") {
		t.Errorf("validateRedirectURL rejected a resolver-allowed custom domain: %v", err)
	}
}

// TestValidateRedirectURL_UnknownHostWithoutResolver_StillRejected pins that
// an unconfigured host is rejected at the allowlist step even after the
// DomainAllowed fallback is wired in — the fallback only widens the
// allowlist for admin-configured custom domains, it never opens it up.
func TestValidateRedirectURL_UnknownHostWithoutResolver_StillRejected(t *testing.T) {
	registry.SetDomainResolver(nil)
	err := validateRedirectURL("https://evil.example.com/x")
	if err == nil || !strings.Contains(err.Error(), "not in the LostFilm allowlist") {
		t.Errorf("validateRedirectURL(unknown host) err = %v, want allowlist rejection", err)
	}
}
