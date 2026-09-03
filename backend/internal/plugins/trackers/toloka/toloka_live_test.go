//go:build live

// Live verification of the toloka plugin against the real site. Skipped by
// every ordinary `go test` run; opt in with `-tags=live`. No workflow sets
// this tag (ci.yml runs untagged, e2e.yml uses `-tags=e2e`), so it cannot
// run in CI.
//
// Everything on Toloka is behind login, so this test needs a real account.
// Credentials come from the environment and are never stored in the repo:
//
//	MARAUDER_TOLOKA_USERNAME=... MARAUDER_TOLOKA_PASSWORD=... \
//	  go test -tags=live -run TestLive -v ./internal/plugins/trackers/toloka/...
//
// MARAUDER_TOLOKA_TOPIC optionally overrides the release URL, which will
// eventually be pruned by the tracker; when that happens the checks below
// fail on a missing torrent block and the URL needs refreshing rather than
// the plugin needing a fix.
package toloka

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const defaultLiveTopic = "https://toloka.to/t699998"

func liveTopicURL() string {
	if u := os.Getenv("MARAUDER_TOLOKA_TOPIC"); u != "" {
		return u
	}
	return defaultLiveTopic
}

// liveCreds builds a credential the way the scheduler does: SecretEnc holds
// the DECRYPTED password by the time a plugin sees it (scheduler.go decrypts
// into that field before calling Login).
func liveCreds(t *testing.T) *domain.TrackerCredential {
	t.Helper()
	user, pass := os.Getenv("MARAUDER_TOLOKA_USERNAME"), os.Getenv("MARAUDER_TOLOKA_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("set MARAUDER_TOLOKA_USERNAME and MARAUDER_TOLOKA_PASSWORD to run the live Toloka checks")
	}
	return &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  user,
		SecretEnc: []byte(pass),
	}
}

// livePlugin builds an unauthenticated plugin. Prefer liveLoggedIn: Toloka
// throttles aggressively (six requests inside three seconds was enough to
// earn an HTTP 429, measured 2026-09-03), so the suite logs in ONCE and
// shares that session rather than re-authenticating per test — which is also
// what the scheduler does in production.
func livePlugin() *plugin {
	return &plugin{sessions: forumcommon.New(), domain: defaultDomain}
}

var (
	loginOnce   sync.Once
	sharedPlug  *plugin
	sharedCreds *domain.TrackerCredential
	sharedErr   error
)

func liveLoggedIn(t *testing.T) (*plugin, *domain.TrackerCredential) {
	t.Helper()
	creds := liveCreds(t)
	loginOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		p := livePlugin()
		if err := p.Login(ctx, creds); err != nil {
			sharedErr = err
			return
		}
		sharedPlug, sharedCreds = p, creds
	})
	if sharedErr != nil {
		t.Fatalf("shared Login: %v", sharedErr)
	}
	// A courtesy gap between network-touching tests.
	time.Sleep(politeGap)
	return sharedPlug, sharedCreds
}

// politeGap spaces the network-touching tests out. Toloka throttles hard:
// the whole suite makes roughly a dozen requests, and at a 2s gap it earned
// an HTTP 429 partway through (measured 2026-09-03). If you still see a
// rate-limit error, wait a minute and re-run rather than assuming a
// selector broke.
const politeGap = 8 * time.Second

func TestLive_Login_EstablishesSession(t *testing.T) {
	p, creds := liveLoggedIn(t)
	id, ok := sessionUserID(p.session(creds), p.baseURL())
	if !ok {
		t.Fatal("Login reported success but the jar carries no authenticated userid")
	}
	t.Logf("logged in as userid=%d", id)
}

// TestLive_Login_RejectsWrongPassword is the regression this plugin most
// needed: a successful login answers 302 with an EMPTY body, so the previous
// body-substring check could never see a failure and reported a rejected
// password as success.
func TestLive_Login_RejectsWrongPassword(t *testing.T) {
	creds := liveCreds(t)
	time.Sleep(politeGap)
	bad := *creds
	bad.SecretEnc = []byte("definitely-not-the-password-9x7")

	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := p.Login(ctx, &bad)
	if err == nil {
		t.Fatal("a wrong password must not be reported as a successful login")
	}
	t.Logf("rejected as expected: %v", err)
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %v, want it to name the rejected credentials", err)
	}
}

func TestLive_Verify_TrueWhenSignedIn(t *testing.T) {
	p, creds := liveLoggedIn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ok, err := p.Verify(ctx, creds)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("Verify = false straight after a successful Login")
	}
}

// TestLive_Verify_FalseWithoutLogin proves Verify is a real signal rather
// than a constant: the same code path on a never-logged-in session must
// report false.
func TestLive_Verify_FalseWithoutLogin(t *testing.T) {
	liveCreds(t) // gate on the env so this does not run unasked
	time.Sleep(politeGap)
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	anon := &domain.TrackerCredential{UserID: uuid.New(), Username: "nobody"}
	ok, err := p.Verify(ctx, anon)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("Verify = true for a session that never logged in")
	}
}

func TestLive_CheckAndDownload_DeliversRealTorrent(t *testing.T) {
	p, creds := liveLoggedIn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	topic, err := p.Parse(ctx, liveTopicURL())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	check, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("Check: hash=%s display=%q", check.Hash, check.DisplayName)
	if len(check.Hash) != 64 {
		t.Errorf("Check.Hash = %q, want a 64-char sha256 change token", check.Hash)
	}
	if check.DisplayName == "" {
		t.Error("Check.DisplayName is empty")
	}
	if strings.Contains(check.DisplayName, " — ") {
		t.Errorf("DisplayName still carries the forum section: %q", check.DisplayName)
	}

	// Two checks in a row must agree; a token that moves on its own would
	// re-download the release on every tick.
	again, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if again.Hash != check.Hash {
		t.Errorf("change token is unstable: %s then %s", check.Hash, again.Hash)
	}

	payload, err := p.Download(ctx, topic, check, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	t.Logf("Download: bytes=%d fileName=%q", len(payload.TorrentFile), payload.FileName)
	if len(payload.TorrentFile) == 0 {
		t.Fatal("Download returned no torrent bytes")
	}
	if payload.MagnetURI != "" {
		t.Errorf("MagnetURI = %q, want empty — Toloka publishes no magnet", payload.MagnetURI)
	}
	if !strings.HasSuffix(payload.FileName, ".torrent") {
		t.Errorf("FileName = %q, want the uploader's .torrent name", payload.FileName)
	}
	ih, err := infohash.FromTorrent(payload.TorrentFile)
	if err != nil {
		t.Fatalf("the downloaded bytes are not a torrent: %v", err)
	}
	t.Logf("infohash=%s", ih)
}

// TestLive_Check_WithoutLogin_ReportsSessionExpired covers the gate: a guest
// gets a stub page with no torrent block, and reporting that as a parse
// failure would send the user hunting for a selector bug.
func TestLive_Check_WithoutLogin_ReportsSessionExpired(t *testing.T) {
	liveCreds(t)
	time.Sleep(politeGap)
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	anon := &domain.TrackerCredential{UserID: uuid.New(), Username: "nobody"}
	topic, err := p.Parse(ctx, liveTopicURL())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = p.Check(ctx, topic, anon)
	if err == nil {
		t.Fatal("Check must fail without a session")
	}
	t.Logf("got: %v", err)
	if !strings.Contains(err.Error(), "session") {
		t.Errorf("error = %v, want it to blame the session rather than the page", err)
	}
}

func TestLive_Search_ReturnsResults(t *testing.T) {
	p, creds := liveLoggedIn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	results, err := p.Search(ctx, "1080p", creds)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	t.Logf("Search returned %d results", len(results))
	for i, r := range results {
		if i == 5 {
			break
		}
		t.Logf("  [%d] %q size=%q seeders=%d url=%s", i, r.Title, r.Size, r.Seeders, r.URL)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}
	for _, r := range results {
		if r.Title == "" {
			t.Errorf("result with empty title: %+v", r)
		}
		if !p.CanParse(r.URL) {
			t.Errorf("search result URL %q does not parse back into a topic", r.URL)
		}
	}
}

// TestLive_Search_WithoutCredentials_ReportsTheRealReason: tracker.php serves
// a guest the form with zero rows and no error, so "no results" would be a
// lie.
func TestLive_Search_WithoutCredentials_ReportsTheRealReason(t *testing.T) {
	liveCreds(t)
	time.Sleep(politeGap)
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := p.Search(ctx, "1080p", nil); err == nil {
		t.Fatal("a search with no account must not report an empty result set")
	}
}

func TestLive_ResolveMetadata_ReturnsTheReleaseName(t *testing.T) {
	p, creds := liveLoggedIn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	meta, err := p.ResolveMetadata(ctx, liveTopicURL(), creds)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	t.Logf("Metadata: title=%q image=%q", meta.Title, meta.ImageURL)
	if meta.Title == "" {
		t.Error("Title is empty")
	}
	if strings.Contains(meta.Title, " — ") {
		t.Errorf("Title still carries the forum section: %q", meta.Title)
	}
}

// TestLive_ResolveMetadata_WithoutLogin_Fails: a guest gets a stub with an
// empty <title>, so resolving anonymously must report that rather than hand
// back an empty name that looks like a parsing failure.
func TestLive_ResolveMetadata_WithoutLogin_Fails(t *testing.T) {
	liveCreds(t)
	time.Sleep(politeGap)
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	anon := &domain.TrackerCredential{UserID: uuid.New(), Username: "nobody"}
	if _, err := p.ResolveMetadata(ctx, liveTopicURL(), anon); err == nil {
		t.Fatal("ResolveMetadata must fail without a session")
	}
}
