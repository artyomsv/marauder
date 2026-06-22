package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// dupFakeQBit simulates qBittorrent's duplicate-add behaviour, which varies by
// version: some versions answer /torrents/add with 409 Conflict, others with
// 200 "Fails." (verified empirically against linuxserver/qbittorrent 5.1.4), and
// the newer multipart path with a 200 JSON summary carrying failure_count>0.
// `present` independently controls whether /torrents/info reports the infohash,
// so a test can model "rejected because already present" vs "rejected and
// genuinely absent" (a real bad-torrent failure).
type dupFakeQBit struct {
	addStatus int    // status code for /torrents/add
	addBody   string // body for /torrents/add
	present   bool   // whether /torrents/info reports the infohash as present
}

func (f *dupFakeQBit) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("v5.1.4"))
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(f.addStatus)
		_, _ = w.Write([]byte(f.addBody))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f.present {
			// Echo the queried hash, mirroring qBittorrent's server-side filter.
			h := r.URL.Query().Get("hashes")
			_, _ = w.Write([]byte(`[{"hash":"` + h + `","progress":0,"state":"downloading"}]`))
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAdd_DuplicateHandling is the regression matrix for issue #76. A duplicate
// add is rejected by qBittorrent via one of three version-dependent signals
// (409 Conflict, 200 "Fails.", or a 200 JSON summary with failure_count>0). In
// every case the add must be an idempotent success when the payload's infohash
// is already present, and must still surface an error when it is genuinely
// absent (a real bad torrent) — the fix must never mask a true failure.
func TestAdd_DuplicateHandling(t *testing.T) {
	const ih = "be0500f2bac970d77fdfea3f4c63290605f92b41"
	goodMagnet := "magnet:?xt=urn:btih:" + ih + "&dn=dup"
	// A magnet with no xt= cannot yield an infohash, so the presence check is
	// fail-closed and the rejection must stand.
	badMagnet := "magnet:?dn=no-infohash"
	const jsonFailure = `{"added_torrent_ids":[],"success_count":0,"failure_count":1,"pending_count":0}`

	tests := []struct {
		name      string
		addStatus int
		addBody   string
		magnet    string
		present   bool
		wantErr   bool
	}{
		{"409 duplicate, infohash present -> idempotent success", http.StatusConflict, "Conflict", goodMagnet, true, false},
		{`200 "Fails." duplicate, infohash present -> idempotent success`, http.StatusOK, "Fails.", goodMagnet, true, false},
		{"JSON failure_count>0, infohash present -> idempotent success", http.StatusOK, jsonFailure, goodMagnet, true, false},
		{"409 rejection, infohash absent -> error", http.StatusConflict, "Conflict", goodMagnet, false, true},
		{`200 "Fails." rejection, infohash absent -> error`, http.StatusOK, "Fails.", goodMagnet, false, true},
		{"JSON failure_count>0, infohash absent -> error", http.StatusOK, jsonFailure, goodMagnet, false, true},
		{"rejection with undecodable payload -> fail-closed error", http.StatusOK, "Fails.", badMagnet, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := (&dupFakeQBit{addStatus: tt.addStatus, addBody: tt.addBody, present: tt.present}).server(t)
			p := &plugin{sessions: map[string]*session{}}
			cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
			payload := &domain.Payload{MagnetURI: tt.magnet}

			err := p.Add(context.Background(), cfg, payload, domain.AddOptions{})
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected an idempotent success, got: %v", err)
			}
		})
	}
}
