package kinozal

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

const e2eDetailsHTML = `<html><head><title>The Movie / Кино (2026) [BDRip] [1080p] / Кинозал.ТВ</title></head>
<body>
<a href="/logout.php">Выход</a>
<div>Инфо хэш: 0123456789ABCDEF0123456789ABCDEF01234567</div>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "kinozal/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/takelogin.php"):
					http.SetCookie(w, &http.Cookie{Name: "uid", Value: "42"})
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="/logout.php">Выход</a>`))
				case strings.HasPrefix(r.URL.Path, "/details.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eDetailsHTML))
				case strings.HasPrefix(r.URL.Path, "/download.php"):
					w.Header().Set("Content-Type", "application/x-bittorrent")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
				case r.URL.Path == "/":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="/logout.php">Выход</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "kinozal.tv",
				transport: &e2etest.HostRewriteTransport{
					From:           "kinozal.tv",
					To:             testHost,
					StripSubdomain: true,
				},
			}
			return p, "https://kinozal.tv/details.php?id=99999"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password123"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "The Movie",
	})
}
