package trackercreds

import (
	"context"

	"golang.org/x/sync/singleflight"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// attempts deduplicates concurrent session attempts per (tracker, user) across
// the whole process — scheduler workers and the four request-path callers of
// Warm alike, since they all publish into the same per-user plugin session.
var attempts singleflight.Group

// EnsureSession makes creds' tracker session usable: Verify first, Login only
// on a miss, and at most one attempt in flight per (tracker, user).
//
// Verify-first because a login is the expensive, rate-limited request: the
// forum plugins keep one session per user in memory and share it across every
// topic, so a live session needs one GET, not a password POST. Single-flight
// because Verify-first alone does not stop a burst — after a restart or a
// session expiry, every worker holding one of that user's topics sees the same
// cold session at the same moment. Issue #198 measured the scheduler's old
// Login-every-check sending four Tapochek logins inside 20 ms, all answered 503.
//
// An errored Verify still falls through to Login. It says nothing about the
// password, and ErrVerifyUnsupported plugins (AniDub, for a page it cannot
// classify) would otherwise never log in at all. Login's error is returned
// unchanged, so the scheduler still sees ErrSessionExpired.
//
// The shared attempt runs on the first caller's deadline but not its
// cancellation: a caller that gives up returns its own ctx.Err() at once,
// while the others keep waiting for an attempt that is still running on their
// behalf. A later caller with a longer deadline can still see the shared
// attempt time out on the first caller's — acceptable, since every caller's
// budget is a TrackerHTTPTimeout-sized one.
//
// Plugins must keep the session in their own store and not in creds: waiting
// callers get only the error, and proceed with their own copy of creds.
func EnsureSession(ctx context.Context, tr registry.Tracker, creds *domain.TrackerCredential) error {
	wc, ok := tr.(registry.WithCredentials)
	if !ok || creds == nil {
		return nil
	}
	ch := attempts.DoChan(tr.Name()+"\x00"+creds.UserID.String(), func() (any, error) {
		actx, cancel := detach(ctx)
		defer cancel()
		if ok, err := wc.Verify(actx, creds); err == nil && ok {
			return nil, nil
		}
		return nil, wc.Login(actx, creds)
	})
	select {
	case r := <-ch:
		return r.Err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// detach keeps ctx's deadline and values but drops its cancellation.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	d := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(d, deadline)
	}
	return context.WithCancel(d)
}
