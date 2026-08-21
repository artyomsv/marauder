package registry

import "errors"

// ErrNoPendingEpisodes is returned by per-episode trackers (currently
// LostFilm) from Download when there are no more pending episodes to
// fetch. The scheduler's per-episode loop matches it via errors.Is to
// terminate the inner loop cleanly.
//
// Plugins that don't implement per-episode tracking never need this —
// they return one payload from Download and any subsequent call (which
// would only happen if the scheduler loop misbehaved) returns whatever
// the plugin's natural error is for "called twice".
var ErrNoPendingEpisodes = errors.New("no pending episodes")

// ErrCaptchaRequired is returned by a plugin's Login when the tracker
// gates authentication behind a captcha (bot protection), rather than
// rejecting the credentials. Callers should surface an actionable
// message ("solve the captcha in a browser / route via cfsolver")
// instead of the misleading "credentials likely wrong". Plugins wrap it
// with %w so callers can match via errors.Is.
var ErrCaptchaRequired = errors.New("tracker requires a captcha")

// ErrCloudflareChallenge is returned when a tracker's response is a
// Cloudflare interstitial rather than the page (or login result) that was
// requested. It exists because a positive-indicator check — "is the
// logged-in marker present?" — cannot distinguish "wrong password" from "we
// never reached the site at all", and defaulting to the former sends users
// chasing a credential problem that does not exist.
//
// Verified against live RuTracker on 2026-08-01: the block is a managed JS
// challenge, and solving it yields a cf_clearance cookie that CAN be replayed
// by a plain HTTP client provided the request repeats the User-Agent the
// browser used (see the flaresolverr package doc — an earlier note here blamed
// the TLS fingerprint, which was a User-Agent mismatch).
//
// So this error means "the tracker is gated and we had no usable clearance" —
// either no solver is configured or the cached clearance just died. It is not
// "retry later" and not "fix your credentials". When the solver IS configured
// but could not answer, use ErrClearanceUnavailable instead.
//
// Plugins wrap it with %w so callers can match via errors.Is.
var ErrCloudflareChallenge = errors.New("tracker is behind a cloudflare challenge")

// ErrClearanceUnavailable is returned when a request was blocked by a
// Cloudflare challenge AND the configured clearance provider had already
// failed to supply a clearance for it. It names the solver, not the tracker.
//
// The distinction is the whole point. ErrCloudflareChallenge is classified
// `cloudflare`, which tells the user "this tracker needs a browser to get
// through" — true when the solver answered and the tracker is genuinely
// gated, and actively misleading when the solver is simply down. On
// 2026-08-05 FlareSolverr took ~9s to launch Chrome while the scheduler began
// checking 1s after boot; every RuTracker topic failed the mint, fell open
// into a bare request, and was reported as a Cloudflare wall — with a
// 30-minute exponential backoff — while the browser it claimed to need was
// finishing its own startup.
//
// Callers wrapping this MUST include the provider's own error as the cause:
// the scheduler classifies it `solver` (never rotating the tracker's domain on
// it) and treats it as transient, so a topic retries in a minute rather than
// parking for half an hour.
//
// It deliberately does NOT wrap ErrCloudflareChallenge. Plugins retry once on
// that sentinel by invalidating the cached clearance and re-fetching, which is
// pointless here: there is no cached clearance to drop, and a second doomed
// request only adds load while the solver is already unwell.
var ErrClearanceUnavailable = errors.New("cloudflare clearance unavailable")

// ErrClearanceNotConfigured is returned when a request was blocked by a
// Cloudflare challenge and NO clearance provider is installed at all — the
// operator never set MARAUDER_FLARESOLVERR_URL.
//
// It is the third leg of a distinction that decides what the user does next:
//
//	ErrCloudflareChallenge     solver answered, tracker is genuinely gated
//	ErrClearanceUnavailable    solver is configured but could not mint
//	ErrClearanceNotConfigured  there is no solver to ask
//
// Issue #158: none of the shipped deployment stacks wired a solver up, so
// every RuTracker install reported the first of those. That message says the
// tracker "needs a browser to get through", which the reporter reasonably read
// as a browser Marauder would open, and waited. The fix a user can act on is
// "run FlareSolverr and point Marauder at it", and only this sentinel says so.
//
// Deliberately NOT wrapped in ErrClearanceUnavailable, despite both naming the
// solver. That one is classified transient and retried in a minute because a
// failed mint is infrastructure and infrastructure comes back; a missing
// configuration does not come back on its own, so this takes the ordinary
// exponential backoff instead of hammering a tracker forever.
//
// Like ErrClearanceUnavailable it does NOT wrap ErrCloudflareChallenge, so the
// plugin's invalidate-and-refetch retry is skipped: there is no cached
// clearance to drop and no provider to re-mint from.
var ErrClearanceNotConfigured = errors.New("cloudflare clearance not configured")

// ErrSessionExpired is returned by a cookie-session plugin's Login when
// no stored session exists or the stored session no longer authenticates.
// The user must re-run the interactive (captcha) login flow.
var ErrSessionExpired = errors.New("tracker session expired")

// ErrSearchRequiresCredentials is returned by a WithSearch tracker whose
// search page is login-gated (RuTracker's tracker.php) when no credential —
// or no longer-valid session — is available. The search handler surfaces it
// as a per-tracker "needs an account" notice instead of a hard failure.
var ErrSearchRequiresCredentials = errors.New("search requires credentials")

// ErrVerifyUnsupported is returned by a WithCredentials plugin whose Verify
// cannot actually determine whether the session is authenticated — typically
// because nobody has identified a reliable logged-in marker for that tracker
// yet.
//
// It exists because the alternative plugins reached for was `return true, nil`,
// which is indistinguishable from a genuine positive and silently defeats the
// two-signal design in the credentials handler: Login's negative check ("does
// the body contain a known error phrase?") becomes the only gate, and that
// check fails open on any wording it does not recognise. A real report: a user
// entered credentials belonging to a different site entirely, the tracker
// rejected them with HTTP 200 and a phrase the plugin did not match, and the
// UI showed a green "login ok".
//
// A plugin returning this sentinel is saying "the credential may be fine, but
// I did not check". Callers must surface that as its own state — never as
// success.
var ErrVerifyUnsupported = errors.New("this tracker plugin cannot verify credentials")
