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

// ErrSessionExpired is returned by a cookie-session plugin's Login when
// no stored session exists or the stored session no longer authenticates.
// The user must re-run the interactive (captcha) login flow.
var ErrSessionExpired = errors.New("tracker session expired")
