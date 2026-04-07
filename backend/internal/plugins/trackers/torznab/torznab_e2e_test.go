package torznab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <title>Example Indexer</title>
  <item>
    <title>The Show S01E12 1080p WEB-DL DDP5.1 H.264-MARAUDER</title>
    <guid>https://example.com/details/9001</guid>
    <pubDate>Mon, 06 Apr 2026 18:00:00 +0000</pubDate>
    <link>https://example.com/download?id=9001</link>
    <enclosure url="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Show.S01E12" length="3145728000" type="application/x-bittorrent"/>
    <torznab:attr name="seeders" value="42"/>
    <torznab:attr name="infohash" value="0123456789ABCDEF0123456789ABCDEF01234567"/>
  </item>
</channel>
</rss>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "torznab/feed-then-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/api") {
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(sampleFeed))
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := New(&http.Client{
				Timeout: 5 * time.Second,
				Transport: &e2etest.HostRewriteTransport{
					From: "prowlarr.example.com",
					To:   testHost,
				},
			})
			return p, "torznab+https://prowlarr.example.com/api?apikey=K&t=search&q=The+Show"
		},
		ExpectedHash:         "0123456789abcdef0123456789abcdef01234567",
		ExpectedNameContains: "S01E12",
	})
}
