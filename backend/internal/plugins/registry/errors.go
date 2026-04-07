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
