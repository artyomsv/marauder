package rutracker

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

const e2eTopicHTML = `<html>
<head><title>Some Show / Сериал [s01e01-12] [WEBRip] [1080p] :: RuTracker.org</title></head>
<body>
<div id="logged-in-username">alice</div>
<a class="magnet-link" href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=Some.Show.S01">Magnet</a>
<a href="dl.php?t=987654">Скачать .torrent</a>
</body>
</html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "rutracker/login-then-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
					http.SetCookie(w, &http.Cookie{Name: "bb_session", Value: "abc123"})
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div id="logged-in-username">alice</div>`))
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eTopicHTML))
				case r.URL.Path == "/forum/index.php":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div id="logged-in-username">alice</div>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "rutracker.org",
				transport: &e2etest.HostRewriteTransport{
					From: "rutracker.org",
					To:   testHost,
				},
			}
			return p, "https://rutracker.org/forum/viewtopic.php?t=987654"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password123"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Some Show",
	})
}
