//go:build live

// Live verification of the tapochek plugin against the real site. Skipped by
// every ordinary `go test` run; opt in with `-tags=live`. No workflow sets
// this tag (ci.yml runs untagged, e2e.yml uses `-tags=e2e`), so it cannot run
// in CI.
//
// Everything the plugin reads is behind login, so this needs a real account.
// Credentials come from the environment and are never stored in the repo:
//
//	MARAUDER_TAPOCHEK_USERNAME=... MARAUDER_TAPOCHEK_PASSWORD=... \
//	  go test -tags=live -run TestLive -v ./internal/plugins/trackers/tapochek/...
//
// MARAUDER_TAPOCHEK_TOPIC optionally overrides the release URL. When the
// default is eventually pruned by the tracker these checks fail on a missing
// torrent block, and the URL needs refreshing rather than the plugin needing
// a fix.
package tapochek

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/infohash"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const defaultLiveTopic = "https://tapochek.net/viewtopic.php?t=289113"

func liveTopicURL() string {
	if u := os.Getenv("MARAUDER_TAPOCHEK_TOPIC"); u != "" {
		return u
	}
	return defaultLiveTopic
}

// liveCreds builds a credential the way the scheduler does: SecretEnc holds
// the DECRYPTED password by the time a plugin sees it (scheduler.go decrypts
// into that field before calling Login).
func liveCreds(t *testing.T) *domain.TrackerCredential {
	t.Helper()
	user, pass := os.Getenv("MARAUDER_TAPOCHEK_USERNAME"), os.Getenv("MARAUDER_TAPOCHEK_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("set MARAUDER_TAPOCHEK_USERNAME and MARAUDER_TAPOCHEK_PASSWORD to run the live Tapochek checks")
	}
	return &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  user,
		SecretEnc: []byte(pass),
	}
}

func livePlugin() *plugin {
	return &plugin{sessions: forumcommon.New(), domain: defaultDomain}
}

// politeGap spaces requests out. Nothing measured suggests Tapochek throttles
// as hard as Toloka does, but a test that hammers someone's forum to prove a
// parser works is the wrong trade either way.
const politeGap = 2 * time.Second

func TestLive_LoginVerifyCheckDownload(t *testing.T) {
	creds := liveCreds(t)
	p := livePlugin()
	ctx := context.Background()

	if err := p.Login(ctx, creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
	time.Sleep(politeGap)

	ok, err := p.Verify(ctx, creds)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify reported a dead session immediately after a successful login")
	}
	time.Sleep(politeGap)

	topic := &domain.Topic{URL: liveTopicURL()}
	check, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash == "" {
		t.Error("Check produced no change token")
	}
	if check.DisplayName == "" || strings.Contains(check.DisplayName, "&#") {
		t.Errorf("DisplayName = %q — the title must be decoded, not entity-encoded", check.DisplayName)
	}
	// Unlike nnmclub/rutracker/toloka, cleanTitle strips no site suffix,
	// because this template serves none. Pin that: if one ever appears, every
	// topic's name silently grows it and ResolveMetadata stores it.
	for _, unwanted := range []string{" :: ", "Tapochek"} {
		if strings.Contains(check.DisplayName, unwanted) {
			t.Errorf("DisplayName = %q contains %q — the template gained a site suffix, so cleanTitle must now strip it", check.DisplayName, unwanted)
		}
	}
	t.Logf("token=%s name=%q", check.Hash, check.DisplayName)
	time.Sleep(politeGap)

	// The token must be stable across two reads of an unchanged page.
	again, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if again.Hash != check.Hash {
		t.Errorf("token moved on an unchanged page: %s -> %s", check.Hash, again.Hash)
	}
	time.Sleep(politeGap)

	meta, err := p.ResolveMetadata(ctx, topic.URL, creds)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	t.Logf("title=%q poster=%q", meta.Title, meta.ImageURL)
	if meta.Title == "" {
		t.Error("no title resolved")
	}
	if meta.ImageURL != "" && !strings.HasPrefix(meta.ImageURL, "http") {
		t.Errorf("poster is not an http(s) URL: %q", meta.ImageURL)
	}
	time.Sleep(politeGap)

	payload, err := p.Download(ctx, topic, check, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !isTorrent(payload.TorrentFile) {
		t.Fatal("Download did not return a bencoded file")
	}
	if !strings.HasSuffix(payload.FileName, ".torrent") {
		t.Errorf("FileName = %q", payload.FileName)
	}
	// The site publishes no infohash, so this is the first point at which one
	// exists at all — derived from the file, which is what the scheduler does
	// for delivery tracking.
	hash, err := infohash.FromTorrent(payload.TorrentFile)
	if err != nil {
		t.Fatalf("the downloaded file is not a parseable torrent: %v", err)
	}
	t.Logf("downloaded %d bytes, infohash=%s", len(payload.TorrentFile), hash)
}

// TestLive_WrongPasswordIsRejected is the check the old plugin could not make:
// it inspected neither status nor body, so a typo was saved and reported as a
// working account.
//
// It deliberately fails a login against the real account named in the
// environment. Nothing is exposed, but repeated runs could get that account
// throttled or captcha-locked by the tracker, so do not loop it.
func TestLive_WrongPasswordIsRejected(t *testing.T) {
	creds := liveCreds(t)
	bad := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  creds.Username,
		SecretEnc: []byte("definitely-not-the-password-" + uuid.NewString()),
	}
	if err := livePlugin().Login(context.Background(), bad); err == nil {
		t.Fatal("a wrong password was reported as a successful login")
	} else {
		t.Logf("rejected as expected: %v", err)
	}
}

// TestLive_AnonymousCannotReachTheTorrentBlock records the finding the
// rewrite rests on: a guest sees the title and description of a public topic
// but no torrent table, so there is nothing to monitor without an account.
func TestLive_AnonymousCannotReachTheTorrentBlock(t *testing.T) {
	if os.Getenv("MARAUDER_TAPOCHEK_USERNAME") == "" {
		t.Skip("set MARAUDER_TAPOCHEK_USERNAME to opt in to the live Tapochek checks")
	}
	_, err := livePlugin().Check(context.Background(), &domain.Topic{URL: liveTopicURL()}, nil)
	if err == nil {
		t.Fatal("an anonymous check succeeded; the gating finding no longer holds")
	}
	t.Logf("anonymous check failed as expected: %v", err)
}
