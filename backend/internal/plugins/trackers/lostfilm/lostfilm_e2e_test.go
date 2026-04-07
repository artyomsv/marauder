package lostfilm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/extra"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// permissiveRedirectValidator is the test seam used in place of the
// production SSRF allowlist. The httptest servers below run on
// 127.0.0.1, which the production validator (correctly) rejects as a
// non-routable IP. Tests that exercise the redirector chain install
// this validator on the plugin so they can hit the loopback fake.
func permissiveRedirectValidator(string) error { return nil }

// e2eSeriesHTML is the magnet-on-the-page test fixture. It uses the
// real LostFilm attribute format (hyphenated data-code + packed
// data-episode) plus a magnet link as the simplest payload — the test
// pipeline expects a single episode and submits the magnet to qBit.
const e2eSeriesHTML = `<html>
<head><title>The Series :: LostFilm.tv</title></head>
<body>
<a href="logout.php">logout</a>
<div class="series-block">
  <a class="dl" data-code="999-1-42" data-episode="999001042">S01E42</a>
</div>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Series.S01E42">magnet</a>
</body>
</html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "lostfilm/login-then-check-then-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/ajaxik.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`{"success":true}`))
				case strings.HasPrefix(r.URL.Path, "/series/"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eSeriesHTML))
				case r.URL.Path == "/v_search.php":
					// Redirect to a fake destination page that has
					// no .torrent links — the test relies on the
					// magnet fallback path on the series page,
					// not on the v_search redirector. Returning a
					// destination with one fake torrent so we can
					// verify the bytes flow through.
					w.Header().Set("Location", "/dl/the-series-s01e42")
					w.WriteHeader(http.StatusFound)
				case r.URL.Path == "/dl/the-series-s01e42":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="https://example.com/the-series-s01e42-1080p.torrent">1080p</a>`))
				case strings.HasSuffix(r.URL.Path, ".torrent"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce5:test:e"))
				case r.URL.Path == "/my":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "www.lostfilm.tv",
				transport: &e2etest.HostRewriteTransport{
					From:  "www.lostfilm.tv",
					To:    testHost,
					Inner: &allHostsToTest{To: testHost},
				},
				redirectValidator: permissiveRedirectValidator,
			}
			return p, "https://www.lostfilm.tv/series/the-series/"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		// New hash format: 1 episode total, 0 done, 1 pending
		ExpectedHash:         "eps:1/done:0/pending:1",
		ExpectedNameContains: "The Series",
	})
}

// e2eRedirectorSeriesHTML is the "real" shape: hyphenated data-code +
// packed data-episode markers and no inline magnet link. The Download
// path has to walk the v_search redirector chain to reach the .torrent.
const e2eRedirectorSeriesHTML = `<html>
<head><title>Monarch :: LostFilm.tv</title></head>
<body>
<a href="logout.php">logout</a>
<div class="series-block">
  <a class="dl" data-code="791-1-5" data-episode="791001005"><span>S01E05</span></a>
</div>
<div class="series-block">
  <a class="dl" data-code="791-2-1" data-episode="791002001"><span>S02E01</span></a>
</div>
<div class="series-block">
  <a class="dl" data-code="791-2-2" data-episode="791002002"><span>S02E02</span></a>
</div>
</body>
</html>`

const e2eRedirectorDestHTML = `<html><body>
<a href="https://tracktor.in/td.php?id=abc-sd.torrent">Download SD</a>
<a href="https://tracktor.in/td.php?id=abc-1080p_mp4.torrent">Download 1080p_mp4</a>
<a href="https://tracktor.in/td.php?id=abc-1080p.torrent">Download 1080p</a>
</body></html>`

// TestRedirectorFlow drives the full GET /v_search.php?a=<packed> →
// 302 → fetch destination → pick quality → fetch .torrent chain
// end-to-end against an httptest.Server.
func TestRedirectorFlow(t *testing.T) {
	var lastVSearchURL, lastTorrentPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/ajaxik.php"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"success":true}`))
		case strings.HasPrefix(r.URL.Path, "/series/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(e2eRedirectorSeriesHTML))
		case r.URL.Path == "/v_search.php":
			// Real LostFilm uses GET ?a=<packed>; verify both that
			// the request is a GET AND that the `a` query param is
			// present.
			lastVSearchURL = r.URL.String()
			if r.Method != http.MethodGet {
				t.Errorf("v_search method = %q, want GET", r.Method)
			}
			if r.URL.Query().Get("a") == "" {
				t.Errorf("v_search missing ?a= param")
			}
			// Redirect to a destination page on the same server.
			w.Header().Set("Location", "/dl/Monarch_S02E02")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/dl/Monarch_S02E02":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(e2eRedirectorDestHTML))
		case strings.HasPrefix(r.URL.Path, "/td.php"):
			lastTorrentPath = r.URL.RequestURI()
			w.WriteHeader(200)
			_, _ = w.Write([]byte("d8:announce5:test:e"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	testHost := strings.TrimPrefix(srv.URL, "http://")
	rewriter := &e2etest.HostRewriteTransport{
		From:  "www.lostfilm.tv",
		To:    testHost,
		Inner: &allHostsToTest{To: testHost},
	}
	p := &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         rewriter,
		redirectValidator: permissiveRedirectValidator,
	}

	creds := &domain.TrackerCredential{
		UserID:    uuid.New(),
		Username:  "alice",
		SecretEnc: []byte("password"),
	}
	ctx := context.Background()

	if err := p.Login(ctx, creds); err != nil {
		t.Fatalf("Login: %v", err)
	}

	topic, err := p.Parse(ctx, "https://www.lostfilm.tv/series/Monarch_Legacy_of_Monsters/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	topic.Extra["quality"] = "1080p"

	// --- First check: nothing downloaded yet, all 3 episodes pending
	check, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "eps:3/done:0/pending:3" {
		t.Errorf("hash = %q, want eps:3/done:0/pending:3", check.Hash)
	}
	pending := extra.StringSlice(check.Extra, "pending_episodes")
	if len(pending) != 3 {
		t.Fatalf("pending = %v, want 3 episodes", pending)
	}
	// Pending list should be sorted: s01e05, s02e01, s02e02 →
	// packed: 791001005, 791002001, 791002002
	if pending[0] != "791001005" {
		t.Errorf("pending[0] = %q, want 791001005", pending[0])
	}
	if pending[2] != "791002002" {
		t.Errorf("pending[2] = %q, want 791002002", pending[2])
	}

	// --- Download first pending (oldest first)
	payload, err := p.Download(ctx, topic, check, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatal("Download returned empty torrent bytes")
	}
	if !strings.Contains(payload.FileName, "791001005") {
		t.Errorf("filename = %q, want packed id 791001005", payload.FileName)
	}
	// Verify v_search was called with the packed ID
	if !strings.Contains(lastVSearchURL, "a=791001005") {
		t.Errorf("v_search URL = %q, want a=791001005", lastVSearchURL)
	}
	// CRITICAL: assert the actual fetched .torrent matches the
	// requested quality. The naive Contains() matcher would have
	// false-matched 1080p_mp4 here.
	if !strings.Contains(lastTorrentPath, "1080p") || strings.Contains(lastTorrentPath, "1080p_mp4") {
		t.Errorf("fetched torrent path = %q, want plain 1080p (not 1080p_mp4)", lastTorrentPath)
	}

	// --- Mark episode 0 as downloaded, recheck
	topic.Extra["downloaded_episodes"] = []string{"791001005"}
	check2, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check after mark-downloaded: %v", err)
	}
	if check2.Hash != "eps:3/done:1/pending:2" {
		t.Errorf("hash after first download = %q, want eps:3/done:1/pending:2", check2.Hash)
	}
	pending2 := extra.StringSlice(check2.Extra, "pending_episodes")
	if len(pending2) != 2 || pending2[0] != "791002001" {
		t.Errorf("pending after first download = %v, want [791002001, 791002002]", pending2)
	}

	// --- Episode filter: ask for s02e01+ → should hide the s01e05
	// episode but include both s02e* ones
	topic.Extra["downloaded_episodes"] = []string{}
	topic.Extra["start_season"] = 2
	topic.Extra["start_episode"] = 1
	check3, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check with filter: %v", err)
	}
	pending3 := extra.StringSlice(check3.Extra, "pending_episodes")
	if len(pending3) != 2 || pending3[0] != "791002001" {
		t.Errorf("pending with filter = %v, want [791002001, 791002002]", pending3)
	}

	// --- Filter rejects everything: start_season=3
	topic.Extra["start_season"] = 3
	topic.Extra["start_episode"] = 1
	check4, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check with high filter: %v", err)
	}
	pending4 := extra.StringSlice(check4.Extra, "pending_episodes")
	if len(pending4) != 0 {
		t.Errorf("pending with start_season=3 = %v, want empty", pending4)
	}
}

// TestDownloadEmptyPendingReturnsTypedSentinel verifies that Download
// wraps registry.ErrNoPendingEpisodes when the pending list is empty,
// so the scheduler's per-episode loop can match it via errors.Is and
// exit cleanly. This is the cross-track integration point with the
// scheduler refactor in Track A.
func TestDownloadEmptyPendingReturnsTypedSentinel(t *testing.T) {
	p := &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		redirectValidator: permissiveRedirectValidator,
	}
	topic := &domain.Topic{
		TrackerName: "lostfilm",
		URL:         "https://www.lostfilm.tv/series/foo/",
		Extra:       map[string]any{},
	}

	cases := []struct {
		name  string
		check *domain.Check
	}{
		{
			name:  "nil check",
			check: &domain.Check{Hash: "eps:0/done:0/pending:0"},
		},
		{
			name: "empty pending slice",
			check: &domain.Check{
				Hash: "eps:0/done:0/pending:0",
				Extra: map[string]any{
					"pending_episodes": []string{},
				},
			},
		},
		{
			name: "missing pending_episodes key",
			check: &domain.Check{
				Hash:  "eps:0/done:0/pending:0",
				Extra: map[string]any{},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Download(context.Background(), topic, tt.check, nil)
			if err == nil {
				t.Fatal("Download returned nil error, want ErrNoPendingEpisodes")
			}
			if !errors.Is(err, registry.ErrNoPendingEpisodes) {
				t.Errorf("Download error = %v, want errors.Is(err, registry.ErrNoPendingEpisodes)", err)
			}
		})
	}
}

// TestValidateRedirectURL exercises the SSRF allowlist directly. The
// loopback rejection path is critical: it's what stops a compromised
// retre.org from pointing the cookie-bearing follow-up at
// http://localhost:5432.
func TestValidateRedirectURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid lostfilm", "https://www.lostfilm.tv/foo", false},
		{"valid retre", "https://retre.org/td.php?s=abc", false},
		{"valid tracktor", "https://tracktor.in/td.php?id=x.torrent", false},
		{"unknown host", "https://evil.example.com/x", true},
		{"non-http scheme", "file:///etc/passwd", true},
		{"loopback by IP", "http://127.0.0.1/x", true},
		{"private 10.x by IP", "http://10.0.0.1/x", true},
		{"empty url", "", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRedirectURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRedirectURL(%q) err = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestQualityMatcher locks down the qualityMatches helper because the
// real LostFilm labels overlap (1080p is a substring of 1080p_mp4).
func TestQualityMatcher(t *testing.T) {
	cases := []struct {
		label, want string
		match       bool
	}{
		{"Download SD", "SD", true},
		{"Download SD", "1080p", false},
		{"Download SD", "1080p_mp4", false},
		{"Download 1080p", "1080p", true},
		{"Download 1080p", "1080p_mp4", false},
		{"Download 1080p", "SD", false},
		{"Download 1080p_mp4", "1080p_mp4", true},
		{"Download 1080p_mp4", "1080p", false}, // critical: must NOT match
		{"Download 1080p_mp4", "SD", false},
		{"MP4", "1080p_mp4", true},
		{"MP4", "1080p", false},
		// Unknown qualities used to fall through to a substring match.
		// They now return false unconditionally so callers must add
		// new tiers explicitly.
		{"Download 720p", "720p", false},
		{"Download HEVC", "x265", false},
	}
	for _, c := range cases {
		got := qualityMatches(c.label, c.want)
		if got != c.match {
			t.Errorf("qualityMatches(%q, %q) = %v, want %v", c.label, c.want, got, c.match)
		}
	}
}

// allHostsToTest rewrites every external host (e.g. tracktor.in) to the
// test server so the redirector chain stays in-process.
type allHostsToTest struct {
	To string
}

func (a *allHostsToTest) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	req.URL.Host = a.To
	return http.DefaultTransport.RoundTrip(req)
}
