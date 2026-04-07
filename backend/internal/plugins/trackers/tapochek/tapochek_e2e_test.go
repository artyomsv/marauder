package tapochek

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

const e2eTapochekHTML = `<html><head><title>Мультсериал :: Tapochek.net</title></head>
<body>
<div>Info hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
<a href="download.php?id=42">скачать</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "tapochek/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/login.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<div>welcome</div>`))
				case strings.HasPrefix(r.URL.Path, "/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eTapochekHTML))
				case strings.HasPrefix(r.URL.Path, "/download.php"):
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
				domain:   "tapochek.net",
				transport: &e2etest.HostRewriteTransport{
					From: "tapochek.net",
					To:   testHost,
				},
			}
			return p, "https://tapochek.net/viewtopic.php?t=12345"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Мультсериал",
	})
}
