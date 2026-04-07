package lostfilm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const e2eSeriesHTML = `<html>
<head><title>The Series :: LostFilm.tv</title></head>
<body>
<a href="logout.php">logout</a>
<div class="series-block" data-episode="42">Episode 42</div>
<div class="series-block" data-episode="41">Episode 41</div>
<div class="series-block" data-episode="40">Episode 40</div>
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
					From: "www.lostfilm.tv",
					To:   testHost,
				},
			}
			return p, "https://www.lostfilm.tv/series/the-series/"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "ep-42",
		ExpectedNameContains: "The Series",
	})
}

// e2eRedirectorSeriesHTML is the "real" shape: data-code attributes
// per episode and no inline magnet link. The Download path has to walk
// the v_search redirector chain to reach the .torrent.
const e2eRedirectorSeriesHTML = `<html>
<head><title>Monarch :: LostFilm.tv</title></head>
<body>
<a href="logout.php">logout</a>
<div class="series-block">
  <a class="dl" data-code="370:1:5"><span>S01E05</span></a>
</div>
<div class="series-block">
  <a class="dl" data-code="370:2:1"><span>S02E01</span></a>
</div>
<div class="series-block">
  <a class="dl" data-code="370:2:2"><span>S02E02</span></a>
</div>
</body>
</html>`

const e2eRedirectorDestHTML = `<html><body>
<a href="https://tracktor.in/td.php?id=abc-sd.torrent">Download SD</a>
<a href="https://tracktor.in/td.php?id=abc-1080p_mp4.torrent">Download 1080p_mp4</a>
<a href="https://tracktor.in/td.php?id=abc-1080p.torrent">Download 1080p</a>
</body></html>`

// TestRedirectorFlow drives the full POST /v_search.php → 302 → fetch
// destination → pick quality → fetch .torrent chain end-to-end against
// an httptest.Server. This is the closest we can get to validating the
// real LostFilm flow without a live account.
func TestRedirectorFlow(t *testing.T) {
	var lastVSearch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/ajaxik.php"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"success":true}`))
		case strings.HasPrefix(r.URL.Path, "/series/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(e2eRedirectorSeriesHTML))
		case r.URL.Path == "/v_search.php":
			_ = r.ParseForm()
			lastVSearch = r.Form.Encode()
			// Redirect to the destination page (still on the same
			// test server so HostRewriteTransport doesn't help us; we
			// use a relative redirect).
			w.Header().Set("Location", "/dl/Monarch_S02E02")
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/dl/Monarch_S02E02":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(e2eRedirectorDestHTML))
		case strings.HasPrefix(r.URL.Path, "/td.php"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte("d8:announce5:test:e"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	testHost := strings.TrimPrefix(srv.URL, "http://")
	rewriter := &e2etest.HostRewriteTransport{
		From: "www.lostfilm.tv",
		To:   testHost,
		Inner: &allHostsToTest{To: testHost},
	}
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    "www.lostfilm.tv",
		transport: rewriter,
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

	check, err := p.Check(ctx, topic, creds)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "s02e02" {
		t.Errorf("hash = %q, want s02e02", check.Hash)
	}

	payload, err := p.Download(ctx, topic, check, creds)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Fatal("Download returned empty torrent bytes")
	}
	if !strings.Contains(lastVSearch, "c=370") || !strings.Contains(lastVSearch, "s=2") || !strings.Contains(lastVSearch, "e=2") {
		t.Errorf("v_search form = %q, want c=370 s=2 e=2", lastVSearch)
	}
	if !strings.Contains(payload.FileName, "1080p") {
		t.Errorf("filename = %q, want 1080p quality marker", payload.FileName)
	}

	// --- Episode filter: ask for s02e05+ → expect "no match" ---
	topic.Extra["start_season"] = 2
	topic.Extra["start_episode"] = 5
	if _, err := p.Download(ctx, topic, check, creds); err == nil {
		t.Error("expected episode-filter mismatch to error, got nil")
	}

	// --- Episode filter: ask for s02e01+ → still picks the latest s02e02 ---
	topic.Extra["start_season"] = 2
	topic.Extra["start_episode"] = 1
	payload2, err := p.Download(ctx, topic, check, creds)
	if err != nil {
		t.Fatalf("Download with start_season=2 start_episode=1: %v", err)
	}
	if len(payload2.TorrentFile) == 0 {
		t.Error("expected non-empty torrent payload with start_season=2 start_episode=1")
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
