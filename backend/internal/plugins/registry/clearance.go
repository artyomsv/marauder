package registry

import (
	"context"
	"sync"
)

// Clearance is a Cloudflare challenge solution: the cookies a browser earned
// plus the User-Agent it earned them with.
//
// The User-Agent is NOT incidental metadata. Measured against live RuTracker
// on 2026-08-01: cf_clearance is accepted only when the request repeats the
// exact UA that solved the challenge — replaying it with Marauder's own UA, an
// empty UA, or a different browser UA all return 403 Cf-Mitigated. An earlier
// investigation attributed that rejection to Go's TLS fingerprint and
// concluded the cookie could not be replayed outside a browser at all; that is
// wrong, and this pairing is what makes replay work.
type Clearance struct {
	// Cookies is name -> value, e.g. {"cf_clearance": "..."}.
	Cookies map[string]string
	// UserAgent MUST be sent on every request carrying Cookies.
	UserAgent string
}

// Valid reports whether the clearance is usable. Both halves are required:
// either one alone still yields a challenge.
func (c Clearance) Valid() bool { return len(c.Cookies) > 0 && c.UserAgent != "" }

// ClearanceProvider mints Cloudflare clearances. Implemented by the
// FlareSolverr client and installed once at boot, mirroring SetDomainResolver,
// because plugins register themselves from init() and so have no access to
// configuration.
type ClearanceProvider interface {
	// Clearance returns a usable clearance, solving probeURL only when no
	// cached one is available. Results are cached per host.
	//
	// probeURL MUST be a URL Cloudflare actually challenges. A clearance is
	// scoped to the rule that issued it, not to the whole host: minting from
	// RuTracker's root — which redirects to the unchallenged
	// /forum/index.php — yields a cf_clearance that still gets 403 on
	// /forum/login.php (measured 2026-08-01). Pass a page known to be gated.
	Clearance(ctx context.Context, probeURL string) (Clearance, error)
	// InvalidateClearance drops any cached clearance for probeURL's host, so
	// the next Clearance call solves afresh.
	InvalidateClearance(probeURL string)
}

// WithChallengeProbe is an optional capability for a challenge-gated tracker:
// it names a URL that Cloudflare reliably challenges, which is what a clearance
// must be minted from.
//
// It exists so the clearance can be warmed at boot. A cold solve takes on the
// order of ten seconds, which is more than the tracker-search handler's
// per-tracker budget — so without warming, the first search after every restart
// fails while the second succeeds.
type WithChallengeProbe interface {
	Tracker
	ChallengeProbeURL() string
}

var (
	clearanceMu sync.RWMutex
	clearanceP  ClearanceProvider
)

// SetClearanceProvider installs the process-wide provider. Passing nil
// disables it, which is the default: an unconfigured deployment keeps dialling
// trackers directly.
func SetClearanceProvider(p ClearanceProvider) {
	clearanceMu.Lock()
	defer clearanceMu.Unlock()
	clearanceP = p
}

// ClearanceFor returns a clearance obtained by solving probeURL, or the zero
// Clearance when no provider is configured. See ClearanceProvider.Clearance
// for what probeURL must be.
//
// "No provider" is deliberately not an error: callers must degrade to a direct
// dial rather than fail, so deployments without a solver behave exactly as
// they did before this feature existed.
func ClearanceFor(ctx context.Context, probeURL string) (Clearance, error) {
	clearanceMu.RLock()
	p := clearanceP
	clearanceMu.RUnlock()
	if p == nil {
		return Clearance{}, nil
	}
	return p.Clearance(ctx, probeURL)
}

// InvalidateClearance drops the cached clearance for probeURL's host. Safe to
// call when no provider is installed.
func InvalidateClearance(probeURL string) {
	clearanceMu.RLock()
	p := clearanceP
	clearanceMu.RUnlock()
	if p == nil {
		return
	}
	p.InvalidateClearance(probeURL)
}
