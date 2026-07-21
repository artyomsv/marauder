package registry

import (
	"strings"
	"sync"
)

// WithDomains declares the domains a tracker is known to operate on.
// The first entry is the canonical/default domain; the rest are known
// mirrors. Combined with admin-configured custom domains (via the
// resolver) it forms the runtime host allowlist for the plugin.
type WithDomains interface {
	Tracker
	Domains() []string
}

// DomainConfig is the admin-configured domain state for one tracker.
type DomainConfig struct {
	Active string   // "" = plugin default
	Custom []string // admin-added mirror hostnames
}

// DomainResolver returns the configured DomainConfig for a tracker name.
type DomainResolver func(trackerName string) DomainConfig

var (
	domainMu       sync.RWMutex
	domainResolver DomainResolver
)

// SetDomainResolver installs the process-wide domain resolver. Called once
// at boot (and from tests). nil disables overrides.
func SetDomainResolver(r DomainResolver) {
	domainMu.Lock()
	defer domainMu.Unlock()
	domainResolver = r
}

func resolveDomain(trackerName string) DomainConfig {
	domainMu.RLock()
	r := domainResolver
	domainMu.RUnlock()
	if r == nil {
		return DomainConfig{}
	}
	return r(trackerName)
}

// ActiveDomain returns the admin-selected domain for the tracker, or ""
// when none is configured — callers fall back to their compiled default.
func ActiveDomain(trackerName string) string {
	return resolveDomain(trackerName).Active
}

// DomainAllowed reports whether host is one of the tracker's known domains
// or an admin-configured custom domain. Comparison is case-insensitive and
// tolerates a leading "www.".
func DomainAllowed(trackerName, host string, known []string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	if host == "" {
		return false
	}
	for _, d := range known {
		if host == strings.TrimPrefix(strings.ToLower(d), "www.") {
			return true
		}
	}
	for _, d := range resolveDomain(trackerName).Custom {
		if host == strings.TrimPrefix(strings.ToLower(d), "www.") {
			return true
		}
	}
	return false
}
