package newznab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const sampleNZBFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
  <title>NZB Indexer</title>
  <item>
    <title>The Show S01E12 1080p WEB-DL</title>
    <guid>nzb-release-9001</guid>
    <pubDate>Mon, 06 Apr 2026 18:00:00 +0000</pubDate>
    <link>https://nzbgeek.example.com/getnzb/abc</link>
    <enclosure url="https://nzbgeek.example.com/getnzb/abc.nzb" length="3145728000" type="application/x-nzb"/>
    <newznab:attr name="category" value="5040"/>
    <newznab:attr name="size" value="3145728000"/>
  </item>
</channel>
</rss>`

// fakeNZBPayload is a tiny NZB document the fake indexer serves when
// the test client requests the enclosure URL.
const fakeNZBPayload = `<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
<file poster="poster" date="1712430000" subject="The Show S01E12">
<groups><group>alt.binaries.test</group></groups>
<segments><segment bytes="100" number="1">abc@example</segment></segments>
</file>
</nzb>`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "newznab/feed-then-nzb-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/api"):
					w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
					w.WriteHeader(200)
					_, _ = w.Write([]byte(sampleNZBFeed))
				case strings.HasPrefix(r.URL.Path, "/getnzb/"):
					w.Header().Set("Content-Type", "application/x-nzb")
					w.WriteHeader(200)
					_, _ = w.Write([]byte(fakeNZBPayload))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := New(&http.Client{
				Timeout: 5 * time.Second,
				Transport: &e2etest.HostRewriteTransport{
					From: "nzbgeek.example.com",
					To:   testHost,
				},
			})
			return p, "newznab+https://nzbgeek.example.com/api?apikey=K&t=search&q=The+Show"
		},
		ExpectedHash:         "nzb-release-9001",
		ExpectedNameContains: "S01E12",
	})
}
