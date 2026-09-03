package toloka

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// e2eTorrent is a minimal single-file torrent; Download must reject anything
// that is not bencoded, so the pipeline needs real bytes rather than a stub.
const e2eTorrent = "d8:announce8:udp://x/" +
	"4:infod6:lengthi1e4:name1:a12:piece lengthi16384e6:pieces0:ee"

// TestE2E drives the full pipeline on the real captured markup: topic page ->
// torrent block -> download.php -> fake qBittorrent.
//
// It runs without credentials on purpose. The shared HostRewriteTransport
// rewrites req.URL in place, and net/http keys the cookie jar on that URL, so
// a credentialed run here would look logged out for a reason that does not
// exist against the live site. Login, Verify and the session gate are covered
// by the unit tests (which redirect the dial instead) and by
// toloka_live_test.go against the real tracker.
func TestE2E(t *testing.T) {
	registry.SetDomainResolver(nil)
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "toloka/topic-block-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.RawQuery, "id=714902") {
					w.Header().Set("Content-Type", "application/x-bittorrent")
					_, _ = w.Write([]byte(e2eTorrent))
					return
				}
				_, _ = w.Write([]byte(fixtureTopicHTML))
			}))
			t.Cleanup(srv.Close)

			p := &plugin{
				sessions: forumcommon.New(),
				domain:   defaultDomain,
				transport: &e2etest.HostRewriteTransport{
					From: defaultDomain,
					To:   strings.TrimPrefix(srv.URL, "http://"),
				},
			}
			return p, "https://toloka.to/t699998"
		},
		ExpectedNameContains: "Поплавлений метал",
		ExpectTorrentFile:    true,
	})
}
