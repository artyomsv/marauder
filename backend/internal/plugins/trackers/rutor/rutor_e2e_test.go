package rutor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const e2eRutorHTML = `<html><head><title>The Big Movie [1080p] :: Rutor.org</title></head>
<body>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Big.Movie">magnet</a>
</body></html>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "rutor/public-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(e2eRutorHTML))
			}))
			t.Cleanup(srv.Close)

			p := &plugin{httpClient: &http.Client{
				Timeout:   5 * time.Second,
				Transport: &e2etest.HostRewriteTransport{From: "rutor.org", To: stripScheme(srv.URL)},
			}}
			return p, "https://rutor.org/torrent/12345/the.big.movie"
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "Big Movie",
	})
}

func stripScheme(u string) string {
	if len(u) > 7 && u[:7] == "http://" {
		return u[7:]
	}
	return u
}
