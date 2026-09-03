//go:build live

// Live verification of the rutor plugin against the real site. Skipped by
// every ordinary `go test` run; opt in with `-tags=live`. It makes real
// requests to rutor mirrors, so it is deliberately not part of CI — no
// workflow sets this tag (ci.yml runs untagged, e2e.yml uses `-tags=e2e`).
//
//	go test -tags=live -run TestLive -v ./internal/plugins/trackers/rutor/...
//
// The topic ids below are real releases; they will eventually be pruned by
// the tracker, at which point Check fails with a 404 and the ids need
// refreshing rather than the plugin needing a fix.
package rutor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/infohash"
)

// liveTopics covers both live mirrors: they share one id space, so the
// new-rutor.org URL exercises the canonical host rewrite as well.
var liveTopics = []struct {
	name string
	url  string
}{
	{"rutor.info", "https://rutor.info/torrent/1104877/irek-gilmutdinov-privet-magija!-kniga-4-labirint-2026-mp3"},
	{"new-rutor.org", "https://new-rutor.org/torrent/1104871/artem-slastin-polujozh-master-run-kniga-9-2026-mp3/"},
}

func livePlugin() *plugin {
	c := newHTTPClient()
	c.Timeout = 30 * time.Second
	return &plugin{httpClient: c}
}

func TestLive_CanParse_AcceptsLiveAndRetiredHosts(t *testing.T) {
	p := livePlugin()
	for _, tc := range liveTopics {
		if !p.CanParse(tc.url) {
			t.Errorf("%s: CanParse(%s) = false, want true", tc.name, tc.url)
		}
	}
	// The retired mirror must still parse so pre-2026-09-03 topics survive.
	if !p.CanParse("https://rutor.org/torrent/1104877/whatever") {
		t.Error("a legacy rutor.org topic URL must still parse")
	}
}

func TestLive_CheckAndDownload_DeliversRealTorrent(t *testing.T) {
	for _, tc := range liveTopics {
		t.Run(tc.name, func(t *testing.T) {
			p := livePlugin()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			topic, err := p.Parse(ctx, tc.url)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			check, err := p.Check(ctx, topic, nil)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			t.Logf("Check: hash=%s display=%q", check.Hash, check.DisplayName)
			if len(check.Hash) != 40 {
				t.Errorf("Check.Hash = %q, want 40 hex chars", check.Hash)
			}
			if strings.Contains(check.DisplayName, " :: ") {
				t.Errorf("Check.DisplayName still carries mirror branding: %q", check.DisplayName)
			}

			payload, err := p.Download(ctx, topic, check, nil)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			t.Logf("Download: torrentBytes=%d fileName=%q magnet=%q",
				len(payload.TorrentFile), payload.FileName, payload.MagnetURI)
			if len(payload.TorrentFile) == 0 {
				t.Error("Download returned no .torrent bytes — it fell back to the magnet")
			}
			if payload.MagnetURI != "" {
				t.Errorf("MagnetURI = %q, want empty alongside a .torrent", payload.MagnetURI)
			}

			got, err := infohash.FromPayload(payload.MagnetURI, payload.TorrentFile)
			if err != nil {
				t.Fatalf("infohash: %v", err)
			}
			if got != check.Hash {
				t.Errorf("payload infohash %s != Check.Hash %s", got, check.Hash)
			}
		})
	}
}

// TestLive_MagnetFallback_CarriesTrackers checks the degraded path against
// real markup: the unit tests use a fixture, and the fallback magnet is
// never observed by TestLive_CheckAndDownload (the file wins there).
func TestLive_MagnetFallback_CarriesTrackers(t *testing.T) {
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	target, err := p.canonicalURL(liveTopics[0].url)
	if err != nil {
		t.Fatalf("canonicalURL: %v", err)
	}
	body, err := p.fetch(ctx, target)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	magnet := magnetFrom(body)
	t.Logf("magnet: %s", magnet)
	if magnet == "" {
		t.Fatal("no magnet found on a live topic page")
	}
	if !strings.Contains(magnet, "&tr=") {
		t.Errorf("magnet carries no tracker; a hash-only magnet needs DHT to resolve: %q", magnet)
	}
	if strings.Contains(magnet, "&amp;") {
		t.Errorf("magnet is still HTML-escaped: %q", magnet)
	}
	if _, err := infohash.FromMagnet(magnet); err != nil {
		t.Errorf("magnet infohash unreadable: %v", err)
	}
}

func TestLive_ResolveMetadata_ReturnsTitleAndPoster(t *testing.T) {
	for _, tc := range liveTopics {
		t.Run(tc.name, func(t *testing.T) {
			p := livePlugin()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			meta, err := p.ResolveMetadata(ctx, tc.url, nil)
			if err != nil {
				t.Fatalf("ResolveMetadata: %v", err)
			}
			t.Logf("Metadata: title=%q image=%q", meta.Title, meta.ImageURL)
			if meta.Title == "" {
				t.Error("Title is empty")
			}
			if strings.Contains(meta.Title, " :: ") {
				t.Errorf("Title still carries mirror branding: %q", meta.Title)
			}
			if meta.ImageURL != "" && !strings.HasPrefix(meta.ImageURL, "https://") {
				t.Errorf("ImageURL is not absolute: %q", meta.ImageURL)
			}
		})
	}
}

func TestLive_Search_ReturnsResults(t *testing.T) {
	p := livePlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := p.Search(ctx, "Мастер Рун", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	t.Logf("Search returned %d results", len(results))
	for i, r := range results {
		if i == 5 {
			break
		}
		t.Logf("  [%d] %q size=%q seeders=%d url=%s", i, r.Title, r.Size, r.Seeders, r.URL)
	}
	if len(results) == 0 {
		t.Error("Search returned no results")
	}
	// A search result must feed straight back into the create pipeline.
	for _, r := range results {
		if !p.CanParse(r.URL) {
			t.Errorf("search result URL %q does not parse", r.URL)
		}
	}
}
