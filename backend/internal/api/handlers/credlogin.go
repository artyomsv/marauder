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
	case errors.Is(err, registry.ErrCloudflareChallenge):
		return "blocked by Cloudflare — the challenge solver is unavailable or its clearance was rejected"
	case errors.Is(err, registry.ErrSessionExpired):
		return "the stored session has expired — sign in again to refresh it"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled),
		isTimeout(err):
		return "the tracker did not respond in time — it may be down or rate-limiting; try again shortly"
	case isUnreachable(err):
		return "the tracker could not be reached — check your network or the tracker's status"
	}
	return err.Error()
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
