package anidub

import (
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

// Mirrors the live topic-page shape: title inside <span id="news-title">, a
// torrent block with id/filename/size, and NO data-hash — the site publishes
// no infohash on the page, so the change token is derived from the block.
const e2eAnidubHTML = `<html><head>
<meta property="og:title" content="Аниме сериал [HDTVRip] [1080p]" />
</head><body>
<h1><span id="news-title">Аниме сериал [HDTVRip] [1080p]</span></h1>
<span class="poster"><img src="/uploads/posts/poster.jpg" alt=""></span>
<div id='torrent_42_info'>
<a href="/engine/download.php?id=42">скачать</a>
<div class="list down">
Раздают: <span class="li_distribute_m">3</span> Качают: <span class="li_swing_m">1</span> Размер: <span class="red">1.20 GB</span>
</div>
<div class="list torrentname">
<span>Имя файла:</span> <span class="red" title="&#091;AniDub&#093;_Series.torrent">n</span>
</div></div>
</body></html>`

// e2eLoggedInHTML is the logout link a signed-in session sees on every page —
// the marker Verify keys on.
const e2eLoggedInHTML = `<a href="/index.php?action=logout">Выход</a>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "anidub/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				// Verify fetches the site root on the established session; a
				// signed-in DLE page carries the logout link.
				case r.URL.Path == "/":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eLoggedInHTML))
				case strings.HasPrefix(r.URL.Path, "/index.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div>welcome alice</div>` + e2eLoggedInHTML))
				case strings.HasPrefix(r.URL.Path, "/engine/download.php"):
					w.Header().Set("Content-Type", "application/x-bittorrent")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
				default:
					// Topic pages: any other path returns the fixture HTML
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eAnidubHTML))
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "tr.anidub.com",
				transport: &e2etest.HostRewriteTransport{
					From: "tr.anidub.com",
					To:   testHost,
				},
			}
			return p, "https://tr.anidub.com/anime/2026/the-series.html"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		// Golden value: sha256 of the fixture's torrent-block fingerprint
		// ("id=42|name=…|size=1.20 GB"). Pinned so a change to how the token
		// is derived has to be a deliberate edit, not a silent drift.
		ExpectedHash:         "a2008d120e315904fd969910f6106b4c20edc66153fe34adfce6cee846957666",
		ExpectedNameContains: "сериал",
	})
}
