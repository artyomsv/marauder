package rutor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// e2eInfohash is the infohash of realTorrent (see rutor_test.go). The topic
// page's magnet must carry the same hash, because Download only accepts a
// .torrent whose infohash matches the magnet Check derived the topic hash
// from.
const e2eInfohash = "c25231b1dfd77b0e2cef7bb81ea2d66967904f9d"

const e2eRutorHTML = `<html><head><title>rutor.info :: The Big Movie [1080p]</title></head>
<body>
<a href="magnet:?xt=urn:btih:` + e2eInfohash + `&amp;dn=The.Big.Movie">magnet</a>
<table id="details"><tr><td><img src="https://img.example/poster.jpg" /></td></tr></table>
</body></html>`

// TestE2E drives the full public pipeline: topic page -> magnet -> the real
// .torrent from the d.* download host -> fake qBittorrent. StripSubdomain
// is what routes d.rutor.info at the same test server, mirroring kinozal's
// dl.* host.
func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "rutor/public-torrent-file-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/download/") {
					w.Header().Set("Content-Type", "application/x-bittorrent")
					_, _ = w.Write([]byte(realTorrent))
					return
				}
				w.WriteHeader(200)
				_, _ = w.Write([]byte(e2eRutorHTML))
			}))
			t.Cleanup(srv.Close)

			p := &plugin{httpClient: &http.Client{
				Timeout: 5 * time.Second,
				Transport: &e2etest.HostRewriteTransport{
					From:           "rutor.info",
					To:             stripScheme(srv.URL),
					StripSubdomain: true,
				},
			}}
			return p, "https://rutor.info/torrent/12345/the.big.movie"
		},
		ExpectedHash:         e2eInfohash,
		ExpectedNameContains: "Big Movie",
		ExpectTorrentFile:    true,
	})
}

func stripScheme(u string) string {
	if len(u) > 7 && u[:7] == "http://" {
		return u[7:]
	}
	return u
}
