package anilibria

import (
	"context"
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

const (
	aniLibertyHEVCHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	aniLibertyAVCHash  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestAniLibertyE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "aniliberty/v1-api-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			return newAniLibertyTestPlugin(t, false), "https://aniliberty.top/anime/releases/release/grand-blue-season-3"
		},
		ExpectedHash:         aniLibertyAVCHash,
		ExpectedNameContains: "Необъятный океан 3",
	})
}

func TestAniLibertyKeepsSonarrVariantWhenOriginalHashChanges(t *testing.T) {
	p := newAniLibertyTestPlugin(t, true)
	topic, err := p.Parse(context.Background(), "https://aniliberty.top/anime/releases/release/grand-blue-season-3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	topic.Extra[sonarrInfoHashKey] = "cccccccccccccccccccccccccccccccccccccccc"
	topic.Extra[sonarrSourceTitleKey] = "Grand Blue Season 3 S03E01-E02 RUS / Необъятный океан 3 / Grand Blue Season 3 - AniLiberty.TOP [WEB-DL 1080p][AVC][1-2]"

	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != aniLibertyAVCHash {
		t.Fatalf("Check Hash = %q, want Sonarr's AVC variant %q", check.Hash, aniLibertyAVCHash)
	}
}

func TestAniLibertyURLMatching(t *testing.T) {
	p := &plugin{}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://aniliberty.top/anime/releases/release/grand-blue-season-3", true},
		{"https://www.aniliberty.top/anime/releases/release/grand-blue-season-3/", true},
		{"https://aniliberty.top.evil.example/anime/releases/release/grand-blue-season-3", false},
		{"https://aniliberty.top/anime/releases", false},
	}
	for _, test := range tests {
		if got := p.CanParse(test.url); got != test.want {
			t.Errorf("CanParse(%q) = %v, want %v", test.url, got, test.want)
		}
	}
}

func TestAniLibertyVariantKey(t *testing.T) {
	tests := map[string]string{
		"Title [WEB-DL 1080p][AVC][1-2]":       "Title [WEB-DL 1080p][AVC]",
		"Title [WEB-DL 1080p][HEVC][E01-E02]":  "Title [WEB-DL 1080p][HEVC]",
		"Title [WEB-DL 1080p][AVC][Episode 1]": "Title [WEB-DL 1080p][AVC]",
		"Title [WEB-DL 1080p][AVC]":            "Title [WEB-DL 1080p][AVC]",
	}
	for label, want := range tests {
		if got := aniLibertyVariantKey(label); got != want {
			t.Errorf("aniLibertyVariantKey(%q) = %q, want %q", label, got, want)
		}
	}
}

func newAniLibertyTestPlugin(t *testing.T, hevcNewest bool) *plugin {
	t.Helper()
	hevcUpdated := "2026-07-15T14:02:50Z"
	avcUpdated := "2026-07-17T08:59:19Z"
	if hevcNewest {
		hevcUpdated = "2026-07-18T08:59:19Z"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/app/search/releases":
			_, _ = w.Write([]byte(`[
				{"id":10241,"alias":"grand-blue-season-3","name":{"main":"Необъятный океан 3"}},
				{"id":8721,"alias":"grand-blue","name":{"main":"Необъятный океан"}}
			]`))
		case "/api/v1/anime/torrents/release/10241":
			_, _ = w.Write([]byte(`[
				{
					"hash":"` + aniLibertyHEVCHash + `",
					"label":"Grand Blue Season 3 - AniLiberty.TOP [WEB-DL 1080p][HEVC][1-2]",
					"magnet":"magnet:?xt=urn:btih:` + aniLibertyHEVCHash + `",
					"updated_at":"` + hevcUpdated + `",
					"release":{"id":10241,"alias":"grand-blue-season-3","name":{"main":"Необъятный океан 3"}}
				},
				{
					"hash":"` + aniLibertyAVCHash + `",
					"label":"Grand Blue Season 3 - AniLiberty.TOP [WEB-DL 1080p][AVC][1-2]",
					"magnet":"magnet:?xt=urn:btih:` + aniLibertyAVCHash + `",
					"updated_at":"` + avcUpdated + `",
					"release":{"id":10241,"alias":"grand-blue-season-3","name":{"main":"Необъятный океан 3"}}
				}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &plugin{
		httpClient:        &http.Client{Timeout: 5 * time.Second},
		aniLibertyAPIBase: srv.URL + "/api/v1",
	}
}
