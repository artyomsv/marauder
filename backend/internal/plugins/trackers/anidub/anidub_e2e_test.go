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

const e2eAnidubHTML = `<html>
<body>
<h1>Аниме сериал [HDTVRip] [1080p]</h1>
<div data-hash="0123456789ABCDEF0123456789ABCDEF01234567">hash here</div>
<a href="/engine/download.php?id=42">скачать</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "anidub/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/index.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div>welcome alice</div>`))
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
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "сериал",
	})
}
