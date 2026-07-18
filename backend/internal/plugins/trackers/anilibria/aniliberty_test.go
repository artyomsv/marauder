package anilibria

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

const aniLibertyTestHash = "0123456789abcdef0123456789abcdef01234567"

func TestDecodeAniLibertyTorrents(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantLen int
		wantErr string
	}{
		{name: "array", body: `[{"hash":"a"},{"hash":"b"}]`, wantLen: 2},
		{name: "object", body: `{"hash":"a"}`, wantLen: 1},
		{name: "empty", body: " \n\t", wantErr: "empty response"},
		{name: "unsupported", body: "null", wantErr: "unsupported response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeAniLibertyTorrents([]byte(test.body))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeAniLibertyTorrents: %v", err)
			}
			if len(got) != test.wantLen {
				t.Fatalf("decoded %d torrents, want %d", len(got), test.wantLen)
			}
		})
	}
}

func TestDecodeAniLibertyReleasesAcceptsArrayOrObject(t *testing.T) {
	for name, body := range map[string]string{
		"array":  `[{"id":42,"alias":"example"}]`,
		"object": `{"id":42,"alias":"example"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeAniLibertyReleases([]byte(body))
			if err != nil {
				t.Fatalf("decodeAniLibertyReleases: %v", err)
			}
			if len(got) != 1 || got[0].ID != 42 || got[0].Alias != "example" {
				t.Fatalf("unexpected releases: %#v", got)
			}
		})
	}
}

func TestFindAniLibertyReleaseNotFound(t *testing.T) {
	p := newAniLibertyBodyPlugin(t, []byte(`[{"id":7,"alias":"other"}]`))

	_, err := p.findAniLibertyRelease(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), `release "missing" not found`) {
		t.Fatalf("error = %v, want release-not-found error", err)
	}
}

func TestFetchAniLibertyTorrentsFiltersInvalidRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*aniLibertyTorrent)
	}{
		{name: "release id mismatch", mutate: func(torrent *aniLibertyTorrent) {
			torrent.Release.ID++
		}},
		{name: "release alias mismatch", mutate: func(torrent *aniLibertyTorrent) {
			torrent.Release.Alias = "other"
		}},
		{name: "invalid hash", mutate: func(torrent *aniLibertyTorrent) {
			torrent.Hash = "not-a-hash"
		}},
		{name: "empty label", mutate: func(torrent *aniLibertyTorrent) {
			torrent.Label = ""
		}},
		{name: "magnet hash mismatch", mutate: func(torrent *aniLibertyTorrent) {
			torrent.Magnet = "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{name: "missing updated at", mutate: func(torrent *aniLibertyTorrent) {
			torrent.UpdatedAt = time.Time{}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			torrent := validAniLibertyTestTorrent()
			test.mutate(&torrent)
			body, err := json.Marshal([]aniLibertyTorrent{torrent})
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			p := newAniLibertyBodyPlugin(t, body)

			_, err = p.fetchAniLibertyTorrents(context.Background(), 42, "example")
			if err == nil || !strings.Contains(err.Error(), "release has no valid torrents") {
				t.Fatalf("error = %v, want invalid-torrent error", err)
			}
		})
	}
}

func TestFetchAniLibertyTorrentsAcceptsBase32Magnet(t *testing.T) {
	torrent := validAniLibertyTestTorrent()
	rawHash, err := hex.DecodeString(torrent.Hash)
	if err != nil {
		t.Fatalf("decode fixture hash: %v", err)
	}
	base32Hash := base32.StdEncoding.EncodeToString(rawHash)
	torrent.Magnet = "magnet:?xt=urn:btih:" + base32Hash
	body, err := json.Marshal([]aniLibertyTorrent{torrent})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	p := newAniLibertyBodyPlugin(t, body)

	got, err := p.fetchAniLibertyTorrents(context.Background(), 42, "example")
	if err != nil {
		t.Fatalf("fetchAniLibertyTorrents: %v", err)
	}
	if len(got) != 1 || got[0].Hash != aniLibertyTestHash {
		t.Fatalf("unexpected torrents: %#v", got)
	}
}

func TestSelectAniLibertyTorrentDoesNotMutateInput(t *testing.T) {
	older := validAniLibertyTestTorrent()
	older.Hash = "1111111111111111111111111111111111111111"
	older.UpdatedAt = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	newer := validAniLibertyTestTorrent()
	newer.Hash = "2222222222222222222222222222222222222222"
	newer.UpdatedAt = older.UpdatedAt.Add(time.Hour)
	torrents := []aniLibertyTorrent{older, newer}

	selected, err := selectAniLibertyTorrent(torrents, "", "")
	if err != nil {
		t.Fatalf("selectAniLibertyTorrent: %v", err)
	}
	if selected.Hash != newer.Hash {
		t.Fatalf("selected hash = %q, want %q", selected.Hash, newer.Hash)
	}
	if torrents[0].Hash != older.Hash || torrents[1].Hash != newer.Hash {
		t.Fatalf("input order mutated: %#v", torrents)
	}
}

func TestSelectAniLibertyTorrentFailsClosedOnMissingVariant(t *testing.T) {
	torrent := validAniLibertyTestTorrent()

	_, err := selectAniLibertyTorrent(
		[]aniLibertyTorrent{torrent},
		"",
		"Example / Different [HEVC][1-2]",
	)
	if err == nil || !strings.Contains(err.Error(), "no torrent variant matches") {
		t.Fatalf("error = %v, want missing-variant error", err)
	}
}

func TestSelectAniLibertyTorrentMatchesVariantKeyAnywhereInSourceTitle(t *testing.T) {
	avc := validAniLibertyTestTorrent()
	hevc := validAniLibertyTestTorrent()
	hevc.Hash = "89abcdef0123456789abcdef0123456789abcdef"
	hevc.Label = "Example - AniLiberty.TOP [WEB-DL 1080p][HEVC][1-2]"
	hevc.Magnet = "magnet:?xt=urn:btih:" + hevc.Hash

	// An indexer that appends its own decoration after the AniLiberty label:
	// the last-" / "-segment heuristic yields no exact variant-key match, but
	// the HEVC torrent's variant key still appears inside the source title.
	sourceTitle := "Example - AniLiberty.TOP [WEB-DL 1080p][HEVC][1-2] (batch repack)"

	selected, err := selectAniLibertyTorrent([]aniLibertyTorrent{avc, hevc}, "", sourceTitle)
	if err != nil {
		t.Fatalf("selectAniLibertyTorrent: %v", err)
	}
	if selected.Hash != hevc.Hash {
		t.Fatalf("selected hash = %q, want HEVC variant %q", selected.Hash, hevc.Hash)
	}
}

func TestSelectAniLibertyTorrentFallbackIgnoresEmptyVariantKeys(t *testing.T) {
	rangeOnly := validAniLibertyTestTorrent()
	rangeOnly.Label = "[1-2]" // variant key strips to "" — must never match everything

	_, err := selectAniLibertyTorrent(
		[]aniLibertyTorrent{rangeOnly},
		"",
		"Example / Different [HEVC][1-2]",
	)
	if err == nil || !strings.Contains(err.Error(), "no torrent variant matches") {
		t.Fatalf("error = %v, want missing-variant error", err)
	}
}

func TestSourceTitleLabel(t *testing.T) {
	tests := map[string]string{
		"Series S01E01 / Example [AVC][1]": "Example [AVC][1]",
		"Example [AVC][1]":                 "Example [AVC][1]",
	}
	for sourceTitle, want := range tests {
		if got := sourceTitleLabel(sourceTitle); got != want {
			t.Errorf("sourceTitleLabel(%q) = %q, want %q", sourceTitle, got, want)
		}
	}
}

func TestDownloadAniLibertyErrors(t *testing.T) {
	p := &plugin{}
	tests := []struct {
		name  string
		check *domain.Check
		want  string
	}{
		{name: "missing check", check: nil, want: "missing check"},
		{name: "missing magnet", check: &domain.Check{Extra: map[string]any{}}, want: "has no magnet"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := p.downloadAniLiberty(test.check)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func validAniLibertyTestTorrent() aniLibertyTorrent {
	return aniLibertyTorrent{
		Hash:      aniLibertyTestHash,
		Label:     "Example - AniLiberty.TOP [WEB-DL 1080p][AVC][1-2]",
		Magnet:    "magnet:?xt=urn:btih:" + aniLibertyTestHash,
		UpdatedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC),
		Release: aniLibertyRelease{
			ID:    42,
			Alias: "example",
		},
	}
}

func newAniLibertyBodyPlugin(t *testing.T, body []byte) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &plugin{
		httpClient:        srv.Client(),
		aniLibertyAPIBase: srv.URL,
	}
}
