package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// captureAddFields runs plugin.Add against a stand-in qBittorrent endpoint and
// returns every multipart field the plugin sent to /api/v2/torrents/add.
func captureAddFields(t *testing.T, cfg Config, payload *domain.Payload, opts domain.AddOptions) map[string]string {
	t.Helper()
	fields := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("v4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		if mr, err := r.MultipartReader(); err == nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				fields[p.FormName()] = string(b)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg.URL = srv.URL
	raw, _ := json.Marshal(cfg)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.Add(context.Background(), raw, payload, opts); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return fields
}

// TestAdd_CategoryField is the regression test for issue #75: a topic's
// category must reach qBittorrent as the native "category" field on
// /api/v2/torrents/add (not only baked into the save path), otherwise tools
// like Sonarr — which discover torrents by qBittorrent category — never see
// it. The category is sanitised with the same helper that nests the save path
// so the label and the folder segment can never diverge.
func TestAdd_CategoryField(t *testing.T) {
	const base = "/data2/torrents2/completed2"
	magnet := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	torrent := &domain.Payload{TorrentFile: []byte("d8:announce..."), FileName: "movie.torrent"}

	tests := []struct {
		name         string
		payload      *domain.Payload
		category     string
		wantPresent  bool
		wantCategory string
		wantSavePath string
	}{
		{
			name:    "magnet with category sends field and nests save path",
			payload: magnet, category: "sonarr-topic",
			wantPresent: true, wantCategory: "sonarr-topic",
			wantSavePath: base + "/sonarr-topic",
		},
		{
			name:    "torrent file with category sends field too",
			payload: torrent, category: "sonarr-topic",
			wantPresent: true, wantCategory: "sonarr-topic",
			wantSavePath: base + "/sonarr-topic",
		},
		{
			name:    "no category omits field",
			payload: magnet, category: "",
			wantPresent: false, wantSavePath: base,
		},
		{
			name:    "whitespace-only category omits field",
			payload: magnet, category: "   ",
			wantPresent: false, wantSavePath: base,
		},
		{
			// A path-ish category must be sanitised identically for the label
			// and the save-path segment, so they stay aligned (issue #75 / the
			// SanitizeCategory consistency fix).
			name:    "path-like category is sanitised to match save path",
			payload: magnet, category: "../tv",
			wantPresent: true, wantCategory: "tv",
			wantSavePath: base + "/tv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := captureAddFields(t,
				Config{Username: "admin", Password: "secret", DownloadDir: base},
				tt.payload,
				domain.AddOptions{Category: tt.category},
			)

			got, present := fields["category"]
			if present != tt.wantPresent {
				t.Errorf("category field present = %v (value %q), want present = %v", present, got, tt.wantPresent)
			}
			if tt.wantPresent && got != tt.wantCategory {
				t.Errorf("category field = %q, want %q", got, tt.wantCategory)
			}
			if sp := fields["savepath"]; sp != tt.wantSavePath {
				t.Errorf("savepath = %q, want %q", sp, tt.wantSavePath)
			}
		})
	}
}
