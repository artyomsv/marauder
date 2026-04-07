package hdclub

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

const e2eHDClubHTML = `<html><head><title>HD Movie 4K :: HD-Club</title></head>
<body>
<a href="logout.php">logout</a>
<div>Info hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "hdclub/login-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/takelogin.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				case strings.HasPrefix(r.URL.Path, "/details.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eHDClubHTML))
				case strings.HasPrefix(r.URL.Path, "/download.php"):
					w.Header().Set("Content-Type", "application/x-bittorrent")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("d8:announce15:http://x/announcee"))
				case r.URL.Path == "/":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := New("hdclub.org", &e2etest.HostRewriteTransport{
				From: "hdclub.org",
				To:   testHost,
			})
			return p, "https://hdclub.org/details.php?id=12345"
		},
		Creds: &domain.TrackerCredential{
			UserID:    uuid.New(),
			Username:  "alice",
			SecretEnc: []byte("password"),
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "HD Movie",
	})
}
