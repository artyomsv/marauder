package registry

import (
	"net/http"
	"sync"
)

// challengeTransport is the process-wide transport used to fetch pages from
// trackers that sit behind a JS challenge (Cloudflare). It is installed once
// at boot, mirroring SetDomainResolver, because plugins register themselves
// from init() and so have no access to configuration.
//
// Only plugins that declare WithCloudflare should consult it — routing an
// unchallenged tracker through a headless browser would be a large and
// pointless cost.
var (
	challengeMu sync.RWMutex
	challengeRT http.RoundTripper
)

// SetChallengeTransport installs the transport used for challenge-gated
// trackers. Passing nil disables it, which is the default: an unconfigured
// deployment keeps dialling trackers directly.
func SetChallengeTransport(rt http.RoundTripper) {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	challengeRT = rt
}

// ChallengeTransport returns the installed transport, or nil when none is
// configured. Callers must treat nil as "fetch directly" rather than an
// error, so the feature stays strictly opt-in.
func ChallengeTransport() http.RoundTripper {
	challengeMu.RLock()
	defer challengeMu.RUnlock()
	return challengeRT
}
