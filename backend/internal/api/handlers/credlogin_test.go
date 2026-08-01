package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// TestExplainLoginFailure covers what the Accounts page actually shows a user.
// The raw plugin error is a Go internals dump — a real report read
// "Unprocessable Entity: login: rutracker login: Post ...: context deadline
// exceeded (Client.Timeout exceeded while awaiting headers)", which looks like
// a Marauder misconfiguration when the tracker was simply down.
func TestExplainLoginFailure(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "client timeout",
			err:         fmt.Errorf("login: rutracker login: Post \"https://x/login.php\": %w (Client.Timeout exceeded while awaiting headers)", context.DeadlineExceeded),
			wantContain: "did not respond in time",
			wantAbsent:  "Client.Timeout",
		},
		{
			name:        "context cancelled",
			err:         fmt.Errorf("login: %w", context.Canceled),
			wantContain: "did not respond in time",
		},
		{
			name:        "cloudflare challenge",
			err:         fmt.Errorf("login: %w", registry.ErrCloudflareChallenge),
			wantContain: "Cloudflare",
		},
		{
			name:        "captcha required",
			err:         fmt.Errorf("login: %w", registry.ErrCaptchaRequired),
			wantContain: "captcha",
		},
		{
			name:        "dns failure",
			err:         fmt.Errorf("login: %w", &net.DNSError{Err: "no such host", Name: "rutracker.org"}),
			wantContain: "could not be reached",
		},
		{
			name:        "wrong password is passed through unchanged",
			err:         errors.New("login: rutracker login failed: invalid credentials (no logged-in marker in response)"),
			wantContain: "invalid credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explainLoginFailure(tt.err)
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("explainLoginFailure() = %q, want it to contain %q", got, tt.wantContain)
			}
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("explainLoginFailure() = %q, must not leak %q", got, tt.wantAbsent)
			}
		})
	}
}

func TestExplainLoginFailure_NilIsEmpty(t *testing.T) {
	if got := explainLoginFailure(nil); got != "" {
		t.Errorf("explainLoginFailure(nil) = %q, want empty", got)
	}
}
