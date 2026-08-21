package handlers

import (
	"context"
	"errors"
	"net"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// explainLoginFailure turns a tracker login error into something worth showing
// a user on the Accounts page.
//
// The raw error is a Go transport dump. A real report read:
//
//	Unprocessable Entity: login: rutracker login: Post
//	"https://rutracker.org/forum/login.php": context deadline exceeded
//	(Client.Timeout exceeded while awaiting headers)
//
// which reads like a Marauder misconfiguration when in fact the tracker's
// origin was serving 502s. The distinction matters because it decides what the
// user does next: wait, fix the solver, solve a captcha, or retype a password.
//
// Only the recognised transport failures are rewritten. Anything else — above
// all a genuine "invalid credentials" — is passed through untouched, because a
// vague message there would be worse than a technical one.
func explainLoginFailure(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, registry.ErrCaptchaRequired):
		return "the tracker is asking for a captcha — use the captcha login to sign in"
	// Checked before ErrCloudflareChallenge: both describe a request the
	// tracker blocked, but only this one has a fix the user can act on, and
	// naming the setting is the whole point. Issue #158 was a reporter waiting
	// for a browser window because the Cloudflare message implied one.
	case errors.Is(err, registry.ErrClearanceNotConfigured):
		return "no Cloudflare solver is configured — this tracker cannot be reached without one. Run a FlareSolverr container and set MARAUDER_FLARESOLVERR_URL to point at it"
	case errors.Is(err, registry.ErrClearanceUnavailable):
		return "the Cloudflare solver did not answer — check the FlareSolverr container is running and reachable from Marauder"
	case errors.Is(err, registry.ErrCloudflareChallenge):
		// The solver answered and the tracker blocked us anyway. The usual
		// cause is not a broken solver but a split egress: the clearance is
		// bound to the requesting address, so a solver behind a different VPN
		// exit mints cookies Marauder cannot use.
		return "blocked by Cloudflare — the solver's clearance was rejected. It must reach the internet from the same public IP as Marauder"
	case errors.Is(err, registry.ErrSessionExpired):
		return "the stored session has expired — sign in again to refresh it"
	// Order is load-bearing: a timeout is also a *net.OpError and so satisfies
	// isUnreachable. Matching it first keeps "timed out" from being reported as
	// "could not be reached", which would send the user after the wrong problem.
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled),
		isTimeout(err):
		return "the tracker did not respond in time — it may be down or rate-limiting; try again shortly"
	case isUnreachable(err):
		// Keep the cause. This is a self-hosted app, so the user IS the
		// operator: "connection refused" on their own box and "no route to
		// host" call for opposite actions, and collapsing both into one
		// sentence is the vagueness this function exists to avoid.
		return "the tracker could not be reached (" + unreachableCause(err) + ") — check your network or the tracker's status"
	}
	return err.Error()
}

// unreachableCause extracts the underlying socket error for display, without
// the address and syscall noise the full *net.OpError carries.
func unreachableCause(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "DNS: " + dns.Err
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return "network error"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isUnreachable(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op)
}
