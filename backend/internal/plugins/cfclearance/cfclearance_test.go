package cfclearance

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type stubProvider struct {
	c   registry.Clearance
	err error
}

func (s stubProvider) Clearance(context.Context, string) (registry.Clearance, error) {
	return s.c, s.err
}

func (s stubProvider) InvalidateClearance(string) {}

func withProvider(t *testing.T, p registry.ClearanceProvider) {
	t.Helper()
	registry.SetClearanceProvider(p)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })
}

func TestIsChallenge(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		want bool
	}{
		{"nil", nil, false},
		{"403 with the marker", &http.Response{StatusCode: 403, Header: http.Header{"Cf-Mitigated": {"challenge"}}}, true},
		{"503 with the marker", &http.Response{StatusCode: 503, Header: http.Header{"Cf-Mitigated": {"challenge"}}}, true},
		// A 403 without the marker is the tracker refusing this user, which
		// is a different problem with a different fix.
		{"403 without the marker", &http.Response{StatusCode: 403, Header: http.Header{}}, false},
		{"200 with the marker", &http.Response{StatusCode: 200, Header: http.Header{"Cf-Mitigated": {"challenge"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsChallenge(tt.resp); got != tt.want {
				t.Errorf("IsChallenge = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCause_ThreeWaySplit is the point of this package: the three outcomes
// have three different fixes, and collapsing them told users to start a
// browser that nothing had been asked to run (issue #158).
func TestCause_ThreeWaySplit(t *testing.T) {
	t.Run("provider errored", func(t *testing.T) {
		withProvider(t, stubProvider{err: errors.New("solver down")})
		boom := errors.New("solver down")
		if got := Cause(boom); !errors.Is(got, registry.ErrClearanceUnavailable) {
			t.Errorf("Cause = %v, want ErrClearanceUnavailable", got)
		}
	})
	t.Run("no provider configured", func(t *testing.T) {
		registry.SetClearanceProvider(nil)
		if got := Cause(nil); !errors.Is(got, registry.ErrClearanceNotConfigured) {
			t.Errorf("Cause = %v, want ErrClearanceNotConfigured", got)
		}
	})
	t.Run("clearance was sent and still blocked", func(t *testing.T) {
		withProvider(t, stubProvider{c: registry.Clearance{
			Cookies: map[string]string{"cf_clearance": "x"}, UserAgent: "UA",
		}})
		if got := Cause(nil); !errors.Is(got, registry.ErrCloudflareChallenge) {
			t.Errorf("Cause = %v, want ErrCloudflareChallenge", got)
		}
	})
}

// TestCause_UnavailableAndNotConfiguredDoNotWrapChallenge keeps
// RetryOnChallenge from re-firing a doomed request: there is no cached
// clearance to drop, and no provider to re-mint from.
func TestCause_UnavailableAndNotConfiguredDoNotWrapChallenge(t *testing.T) {
	withProvider(t, stubProvider{err: errors.New("down")})
	if errors.Is(Cause(errors.New("down")), registry.ErrCloudflareChallenge) {
		t.Error("the unavailable cause must not wrap ErrCloudflareChallenge")
	}
	registry.SetClearanceProvider(nil)
	if errors.Is(Cause(nil), registry.ErrCloudflareChallenge) {
		t.Error("the not-configured cause must not wrap ErrCloudflareChallenge")
	}
}

func TestApply_SeedsTheJarAndReportsTheUserAgent(t *testing.T) {
	withProvider(t, stubProvider{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "token"}, UserAgent: "Mozilla/5.0 (real browser)",
	}})
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://kinozal.me/details.php?id=1")

	c, err := Apply(context.Background(), "kinozal", jar, u, "https://kinozal.me/browse.php")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := UserAgent(c, "Marauder/0.3"); got != "Mozilla/5.0 (real browser)" {
		t.Errorf("UserAgent = %q — the cookie is bound to the UA it was issued for", got)
	}
	var found bool
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "cf_clearance" && ck.Value == "token" {
			found = true
		}
	}
	if !found {
		t.Error("the clearance cookie was not seeded onto the origin")
	}
}

// TestApply_HalfAClearanceIsAFailure: Valid() demands both the cookie and the
// User-Agent, because either alone still yields a challenge.
func TestApply_HalfAClearanceIsAFailure(t *testing.T) {
	withProvider(t, stubProvider{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "token"}, // no UserAgent
	}})
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://kinozal.me/")
	if _, err := Apply(context.Background(), "kinozal", jar, u, "https://kinozal.me/browse.php"); err == nil {
		t.Error("a half clearance from a CONFIGURED provider must be reported as a failure")
	}
}

// TestApply_NoProviderIsNotAnError: plenty of paths are ungated, so the
// caller should simply make the request unadorned.
func TestApply_NoProviderIsNotAnError(t *testing.T) {
	registry.SetClearanceProvider(nil)
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://kinozal.me/")
	c, err := Apply(context.Background(), "kinozal", jar, u, "https://kinozal.me/browse.php")
	if err != nil {
		t.Errorf("Apply with no provider = %v, want nil", err)
	}
	if c.Valid() {
		t.Error("no provider must yield a zero clearance")
	}
	if got := UserAgent(c, "Marauder/0.3"); got != "Marauder/0.3" {
		t.Errorf("UserAgent = %q, want the plugin's own", got)
	}
}

func TestRetryOnChallenge_RetriesOnceThenGivesUp(t *testing.T) {
	withProvider(t, stubProvider{c: registry.Clearance{
		Cookies: map[string]string{"cf_clearance": "x"}, UserAgent: "UA",
	}})
	calls := 0
	_, err := RetryOnChallenge("https://kinozal.me/browse.php", func() (int, error) {
		calls++
		return 0, registry.ErrCloudflareChallenge
	})
	if !errors.Is(err, registry.ErrCloudflareChallenge) {
		t.Errorf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("attempts = %d, want exactly 2 — a second doomed request only loads an unwell solver", calls)
	}
}

func TestRetryOnChallenge_DoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	boom := errors.New("connection refused")
	_, err := RetryOnChallenge("https://kinozal.me/browse.php", func() (int, error) {
		calls++
		return 0, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want 1", calls)
	}
}
