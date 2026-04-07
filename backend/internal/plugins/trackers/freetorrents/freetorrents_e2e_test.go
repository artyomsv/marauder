package freetorrents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const e2eFreetorrentsHTML = `<html><head><title>Big Release :: Free-Torrents.org</title></head>
<body>
<a href="logout">logout</a>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=Big.Release">magnet</a>
<a href="dl.php?id=42">download</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "freetorrents/login-then-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout">logout</a>`))
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eFreetorrentsHTML))
				case r.URL.Path == "/forum/index.php":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := New("free-torrents.org", &e2etest.HostRewriteTransport{
				From: "free-torrents.org",
				To:   testHost,
			})
			return p, "https://free-torrents.org/forum/viewtopic.php?t=12345"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Big Release",
	})
}
