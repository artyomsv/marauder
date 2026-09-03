package kinozal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// Kinozal started answering a plain Go client with a Cloudflare managed
// challenge on 2026-09-03 (/browse.php, /details.php, /get_srv_details.php;
// the site root still returns 200). These tests guard the clearance path that
// answers it. They mirror rutracker_clearance_test.go, which guards the same
// shared code from the other side.

// fakeProvider is a clearance provider that records what it was asked.
type fakeProvider struct {
	mu          sync.Mutex
	c           registry.Clearance
	err         error
	probes      []string
	invalidated []string
}

func (p *fakeProvider) Clearance(_ context.Context, probeURL string) (registry.Clearance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probes = append(p.probes, probeURL)
	return p.c, p.err
}

func (p *fakeProvider) InvalidateClearance(probeURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.invalidated = append(p.invalidated, probeURL)
}

func (p *fakeProvider) snapshot() (probes, invalidated []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.probes...), append([]string(nil), p.invalidated...)
}

func withProvider(t *testing.T, p registry.ClearanceProvider) {
	t.Helper()
	registry.SetClearanceProvider(p)
	t.Cleanup(func() { registry.SetClearanceProvider(nil) })
}

// goodClearance is what a working FlareSolverr returns: the cookie AND the
// User-Agent it was issued for. Either alone still gets challenged.
func goodClearance() registry.Clearance {
	return registry.Clearance{
		Cookies:   map[string]string{"cf_clearance": "minted-token"},
		UserAgent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/128.0.0.0",
	}
}

// recordedRequest is what the server actually received.
type recordedRequest struct {
	path      string
	userAgent string
	clearance string
}

// challengeThenServe builds a plugin whose server answers the first
// `challenges` requests with a Cloudflare interstitial and everything after
// that with body. It returns the plugin and a func yielding what was received.
func challengeThenServe(t *testing.T, challenges int, body string) (*plugin, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		cookie := ""
		if c, err := r.Cookie("cf_clearance"); err == nil {
			cookie = c.Value
		}
		seen = append(seen, recordedRequest{
			path: r.URL.Path, userAgent: r.Header.Get("User-Agent"), clearance: cookie,
		})
		n++
		challenged := n <= challenges
		mu.Unlock()

		if challenged {
			// Exactly what Cloudflare sends: the header is the signal, the
			// body is a decoy page that contains none of the plugin's markers.
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    strings.TrimPrefix(srv.URL, "http://"),
		transport: &schemeRewrite{},
	}
	return p, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

// TestFetch_ChallengedWithNoSolver_BlamesTheMissingSolver is issue #158 for
// kinozal: every shipped stack wired the FlareSolverr container and the env
// var separately and none wired both, so this state was universal. Reporting
// it as "this tracker needs a browser" describes a browser nothing had been
// asked to run — the operator's missing setting is the only fix.
func TestFetch_ChallengedWithNoSolver_BlamesTheMissingSolver(t *testing.T) {
	withProvider(t, nil)
	p, _ := challengeThenServe(t, 99, "")

	_, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil)
	if !errors.Is(err, registry.ErrClearanceNotConfigured) {
		t.Fatalf("err = %v, want ErrClearanceNotConfigured", err)
	}
	if errors.Is(err, registry.ErrCloudflareChallenge) {
		t.Error("must not also read as a plain challenge — that blames the tracker")
	}
}

// TestFetch_ChallengedWithFailedMint_BlamesTheSolver: a configured solver that
// could not mint means the request was doomed before it was made, so the
// tracker's wall says nothing about the tracker.
func TestFetch_ChallengedWithFailedMint_BlamesTheSolver(t *testing.T) {
	boom := errors.New("flaresolverr: connection refused")
	withProvider(t, &fakeProvider{err: boom})
	p, _ := challengeThenServe(t, 99, "")

	_, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil)
	if !errors.Is(err, registry.ErrClearanceUnavailable) {
		t.Fatalf("err = %v, want ErrClearanceUnavailable", err)
	}
	if !errors.Is(err, boom) {
		t.Error("the provider's own error must survive the wrap — it is the only thing that names the real fault")
	}
}

// TestFetch_SendsClearanceCookieAndBrowserUA is the UA binding the whole
// package is about. cf_clearance is bound to the User-Agent it was issued for
// (and the egress IP), NOT to the TLS fingerprint — sending the cookie with
// the plugin's own UA is the same as sending no cookie at all, and that
// mismatch is what made this model look impossible in July.
func TestFetch_SendsClearanceCookieAndBrowserUA(t *testing.T) {
	fp := &fakeProvider{c: goodClearance()}
	withProvider(t, fp)
	p, seen := challengeThenServe(t, 0, fixtureDetailsHTML)

	if _, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].clearance != "minted-token" {
		t.Errorf("cf_clearance = %q, want the minted token to reach the server", reqs[0].clearance)
	}
	if reqs[0].userAgent != goodClearance().UserAgent {
		t.Errorf("User-Agent = %q, want the one the clearance was issued for", reqs[0].userAgent)
	}
	if reqs[0].userAgent == userAgent {
		t.Error("sent the plugin's own UA; the cookie is UA-bound and would be rejected")
	}
}

// TestFetch_ChallengeInvalidatesAndRetriesExactlyOnce: a stale clearance is
// dropped and re-minted once. Once, not in a loop — a second doomed request
// only loads an already-unwell solver.
func TestFetch_ChallengeInvalidatesAndRetriesExactlyOnce(t *testing.T) {
	fp := &fakeProvider{c: goodClearance()}
	withProvider(t, fp)
	p, seen := challengeThenServe(t, 1, fixtureDetailsHTML)

	body, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil)
	if err != nil {
		t.Fatalf("the retry must succeed: %v", err)
	}
	if !strings.Contains(string(body), "Выход") {
		t.Error("the RETRY's body must be the one returned, not the challenge's")
	}
	if reqs := seen(); len(reqs) != 2 {
		t.Errorf("requests = %d, want exactly 2", len(reqs))
	}
	_, invalidated := fp.snapshot()
	if len(invalidated) != 1 {
		t.Errorf("InvalidateClearance calls = %d, want 1 — retrying with the same dead cookie is doomed", len(invalidated))
	}
}

// TestFetch_PlainForbidden_IsAStatusNotAChallenge: the challenge check reads
// Cf-Mitigated, not the status. A bare 403 with no such header is the tracker
// refusing this user and must still surface as a status.
func TestFetch_PlainForbidden_IsAStatusNotAChallenge(t *testing.T) {
	withProvider(t, &fakeProvider{c: goodClearance()})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // no Cf-Mitigated
	}))
	t.Cleanup(srv.Close)
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    strings.TrimPrefix(srv.URL, "http://"),
		transport: &schemeRewrite{},
	}

	_, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil)
	if err == nil {
		t.Fatal("a 403 must be an error")
	}
	if !strings.Contains(err.Error(), "-> 403") {
		t.Errorf("err = %v, want the status in the message (classifyError reads it)", err)
	}
	if errors.Is(err, registry.ErrCloudflareChallenge) || errors.Is(err, registry.ErrClearanceUnavailable) {
		t.Error("a plain 403 must not be reported as a Cloudflare problem")
	}
}

// TestChallengeProbeURL_IsAChallengedPage: a clearance is scoped to the
// Cloudflare RULE that issued it, not to the host. Minting from the
// unchallenged site root yields a cookie that still 403s on /details.php —
// the mistake that made RuTracker's first wiring useless.
func TestChallengeProbeURL_IsAChallengedPage(t *testing.T) {
	fp := &fakeProvider{c: goodClearance()}
	withProvider(t, fp)
	p, _ := challengeThenServe(t, 0, fixtureDetailsHTML)

	if _, err := p.fetch(context.Background(), "https://"+p.effectiveDomain()+"/details.php?id=1", nil); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	probes, _ := fp.snapshot()
	if len(probes) == 0 {
		t.Fatal("the provider was never asked for a clearance")
	}
	if !strings.HasSuffix(probes[0], "/browse.php") {
		t.Errorf("probe = %q, want a genuinely challenged page, not the 200-answering root", probes[0])
	}
}

// TestLogin_ChallengeIsNotASuccessfulLogin is the bug this file exists for.
// A Cloudflare interstitial carries no "Неверный", and before the status and
// challenge checks were added, a 403 set LoggedIn = true — the Test-login
// button showed a green tick for an account that never authenticated.
func TestLogin_ChallengeIsNotASuccessfulLogin(t *testing.T) {
	withProvider(t, nil)
	p, _ := challengeThenServe(t, 99, "")
	creds := &domain.TrackerCredential{
		UserID: uuid.New(), Username: "u", SecretEnc: []byte("p"),
	}

	err := p.Login(context.Background(), creds)
	if err == nil {
		t.Fatal("a challenged login must fail, not report success")
	}
	if !errors.Is(err, registry.ErrClearanceNotConfigured) {
		t.Errorf("err = %v, want the missing solver named", err)
	}
	if p.session(creds).LoggedIn {
		t.Error("LoggedIn was set from a page nothing authenticated against")
	}
}

// TestVerify_ChallengeIsAnErrorNotADeadSession: an interstitial carries no
// "Выход" either way, so reporting it as false sends the user to re-enter
// perfectly good credentials when the fix is a solver.
func TestVerify_ChallengeIsAnErrorNotADeadSession(t *testing.T) {
	withProvider(t, nil)
	p, _ := challengeThenServe(t, 99, "")
	creds := &domain.TrackerCredential{UserID: uuid.New(), Username: "u"}

	ok, err := p.Verify(context.Background(), creds)
	if ok {
		t.Error("a challenged verify must not report a live session")
	}
	if err == nil {
		t.Fatal("a challenged verify must be an error, not a clean false")
	}
	if !errors.Is(err, registry.ErrClearanceNotConfigured) {
		t.Errorf("err = %v, want the missing solver named rather than a dead session", err)
	}
}

// TestHostAllowed_RefusesOffSiteAndItsDownloadSubdomain. This guard matters
// more than an ordinary SSRF barrier here: Apply writes the minted
// cf_clearance into the jar for the target's origin BEFORE the request goes
// out, so an off-site target would be HANDED the clearance, not merely
// fetched.
func TestHostAllowed_RefusesOffSiteAndItsDownloadSubdomain(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := &plugin{domain: "kinozal.me"}
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"https://kinozal.me/details.php?id=1", true},
		{"https://dl.kinozal.me/download.php?id=1", true},
		{"https://kinozal.guru/details.php?id=1", true}, // known mirror
		// Retired: kinozal.tv is in parseDomains so old stored URLs still
		// parse, but it is NOT in knownDomains, so no request is built
		// against it. Rotation landing on a dead host is a one-way trip.
		{"https://kinozal.tv/details.php?id=1", false},
		{"https://evil.com/details.php?id=1", false},
		// The dl. allowance strips one prefix and re-checks the base, so it
		// must not become a way in for an unrelated host.
		{"https://dl.evil.com/download.php?id=1", false},
		{"https://kinozal.me.evil.com/details.php?id=1", false},
		{"http://169.254.169.254/latest/meta-data/", false},
		{"http://localhost:6379/", false},
	} {
		u, err := parseForTest(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := p.hostAllowed(u); got != tc.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestFetch_RefusesOffSiteHost proves the guard runs before anything is
// dialled — no test server is needed.
func TestFetch_RefusesOffSiteHost(t *testing.T) {
	registry.SetDomainResolver(nil)
	withProvider(t, &fakeProvider{c: goodClearance()})
	p := &plugin{sessions: forumcommon.New(), domain: "kinozal.me"}

	_, err := p.fetch(context.Background(), "https://evil.com/details.php?id=1", nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to fetch off-site host") {
		t.Errorf("err = %v, want the off-site refusal", err)
	}
}

// parseForTest keeps the table above readable; url.Parse never fails on these.
func parseForTest(raw string) (*url.URL, error) { return url.Parse(raw) }
