package anilibria

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

const e2eTitleJSON = `{
  "names": {"ru": "Аниме Сериал", "en": "Anime Series"},
  "torrents": {
    "list": [
      {"torrent_id": 100, "quality": {"string": "HDTVRip"}, "url": "/upload/torrents/100.torrent"},
      {"torrent_id": 101, "quality": {"string": "BDRip"},   "url": "/upload/torrents/101.torrent"}
    ]
  }
}`

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "anilibria/json-api-then-torrent-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/v3/title"):
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					_, _ = w.Write([]byte(e2eTitleJSON))
				case strings.HasPrefix(r.URL.Path, "/upload/torrents/"):
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
				httpClient: &http.Client{
					Timeout: 5 * time.Second,
					Transport: &e2etest.HostRewriteTransport{
						From: "anilibria.tv",
						To:   testHost,
					},
				},
				apiBase: srv.URL + "/v3",
			}
			return p, "https://anilibria.tv/release/anime-series.html"
		},
		ExpectedHash: "anilibria-101",
	})
}
