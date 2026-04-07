package unionpeer

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

const e2eUnionpeerHTML = `<html><head><title>Сборник :: Unionpeer.org</title></head>
<body>
<div>Info hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
<a href="dl.php?t=12345">скачать</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "unionpeer/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div>welcome</div>`))
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eUnionpeerHTML))
				case strings.HasPrefix(r.URL.Path, "/forum/dl.php"):
					w.Header().Set("Content-Type", "application/x-bittorrent")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "unionpeer.org",
				transport: &e2etest.HostRewriteTransport{
					From: "unionpeer.org",
					To:   testHost,
				},
			}
			return p, "https://unionpeer.org/forum/viewtopic.php?t=12345"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Сборник",
	})
}
