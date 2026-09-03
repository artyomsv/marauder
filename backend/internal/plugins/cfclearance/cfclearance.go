// Package cfclearance holds the Cloudflare-clearance dance shared by tracker
// plugins whose sites answer a plain Go client with a managed challenge.
//
// It was extracted from the rutracker plugin when kinozal started being
// challenged too (measured 2026-09-03: /browse.php, /details.php and
// /get_srv_details.php all answer 403 with a Cf-Mitigated interstitial, while
// the site root still returns 200). The logic is small but every branch of it
// was paid for by a real incident, so a second copy would be the wrong kind
// of duplication — see Cause for the three-way split that took two of them to
// get right.
//
// The model is NOT to proxy traffic through a browser. A provider (today
// FlareSolverr) solves the challenge once and returns a `cf_clearance` cookie
// plus the User-Agent it was issued for; the plugin replays that PAIR on its
// own requests. Keeping the requests the plugin's own is what lets it submit
// a login and carry a binary .torrent instead of degrading to a magnet.
package cfclearance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// errUnusable marks a provider that answered but supplied a clearance missing
// its cookie or its User-Agent. It never reaches the user: Cause wraps it in
// registry.ErrClearanceUnavailable, which is what "a configured solver could
// not supply a clearance" already means. It exists only so Apply can report
// that state distinctly from having no provider at all, which returns the
// same zero value.
var errUnusable = errors.New("clearance provider returned an unusable clearance")

// Apply seeds jar with the clearance cookie for u's origin and returns the
// clearance so the caller can send the matching User-Agent. A nil error with
// a zero Clearance means no provider is configured — ordinary, and the caller
// should just make the request unadorned, since plenty of paths are ungated.
//
// plugin is used only for logging.
func Apply(ctx context.Context, plugin string, jar http.CookieJar, u *url.URL, probeURL string) (registry.Clearance, error) {
	c, err := registry.ClearanceFor(ctx, probeURL)
	if err != nil {
		log.Warn().Str("plugin", plugin).Err(err).
			Msg("cloudflare clearance unavailable; requesting without one")
		return registry.Clearance{}, err
	}
	if !c.Valid() {
		// Valid() demands BOTH a cookie and the User-Agent it was issued for;
		// either alone still yields a challenge. A provider that answered
		// with half a clearance has therefore failed, and saying so is the
		// only way the caller can tell this apart from having no provider at
		// all. Without the split, Cause fell through to
		// ErrCloudflareChallenge and told the user their solver's clearance
		// had been rejected over an egress-IP mismatch, when nothing had been
		// minted to reject.
		if registry.ClearanceConfigured() {
			return registry.Clearance{}, errUnusable
		}
		return registry.Clearance{}, nil
	}
	cookies := make([]*http.Cookie, 0, len(c.Cookies))
	for name, val := range c.Cookies {
		cookies = append(cookies, &http.Cookie{Name: name, Value: val, Path: "/"})
	}
	jar.SetCookies(&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}, cookies)
	return c, nil
}

// UserAgent returns the User-Agent to send: the one the clearance was issued
// for when there is a usable clearance, otherwise the plugin's own. Sending
// the wrong one is the whole reason a replayed cookie appears "TLS-bound" —
// it is not, it is UA-bound.
func UserAgent(c registry.Clearance, fallback string) string {
	if c.Valid() {
		return c.UserAgent
	}
	return fallback
}

// IsChallenge reports whether resp is a Cloudflare interstitial rather than
// the page. Cloudflare labels these with Cf-Mitigated (403 for the managed
// challenge, 503 for the legacy "checking your browser" page), which is a far
// more reliable signal than sniffing the body for a phrase that changes.
func IsChallenge(resp *http.Response) bool {
	if resp == nil || resp.Header.Get("Cf-Mitigated") == "" {
		return false
	}
	return resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusServiceUnavailable
}

// Cause names the culprit for a request the tracker just blocked. The three
// outcomes are deliberately distinct because they have three different fixes:
//
//   - the provider errored        -> blame the solver, not the tracker
//   - no provider is configured   -> blame the operator's missing setting
//   - a clearance was sent anyway -> blame the tracker (or the egress IP)
//
// Reporting all three as "this tracker needs a browser" was issue #158: it
// described a browser nothing had been asked to run.
func Cause(clearErr error) error {
	if clearErr != nil {
		return fmt.Errorf("%w: %w", registry.ErrClearanceUnavailable, clearErr)
	}
	if !registry.ClearanceConfigured() {
		return registry.ErrClearanceNotConfigured
	}
	return registry.ErrCloudflareChallenge
}

// RetryOnChallenge runs attempt, and on a challenge drops the cached
// clearance and runs it exactly once more. Once, not in a loop: a second
// doomed request only loads an already-unwell solver.
//
// It deliberately reacts to ErrCloudflareChallenge only. The unavailable and
// not-configured causes do not wrap it, so they skip the retry — there is no
// cached clearance to drop, and no provider to re-mint from.
func RetryOnChallenge[T any](probeURL string, attempt func() (T, error)) (T, error) {
	out, err := attempt()
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		return out, err
	}
	registry.InvalidateClearance(probeURL)
	return attempt()
}
