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
// 200 "Fails." (verified empirically against linuxserver/qbittorrent 5.1.4).
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

const dupInfohash = "be0500f2bac970d77fdfea3f4c63290605f92b41"

func dupAdd(t *testing.T, f *dupFakeQBit) error {
	t.Helper()
	srv := f.server(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:" + dupInfohash + "&dn=dup"}
	return p.Add(context.Background(), cfg, payload, domain.AddOptions{})
}

// TestAdd_Duplicate409_AlreadyPresent_IsIdempotentSuccess reproduces issue #76
// for qBittorrent versions that reject a duplicate add with 409 Conflict (the
// version the reporter runs). When the infohash is already present, the retry
// must be an idempotent success, not an error.
func TestAdd_Duplicate409_AlreadyPresent_IsIdempotentSuccess(t *testing.T) {
	if err := dupAdd(t, &dupFakeQBit{addStatus: http.StatusConflict, addBody: "Conflict", present: true}); err != nil {
		t.Fatalf("duplicate 409 with infohash present should be an idempotent success, got: %v", err)
	}
}

// TestAdd_DuplicateFailsBody_AlreadyPresent_IsIdempotentSuccess reproduces the
// same bug on qBittorrent 5.1.4, which signals a duplicate with 200 "Fails."
// rather than 409 (verified empirically). Same expectation: idempotent success.
func TestAdd_DuplicateFailsBody_AlreadyPresent_IsIdempotentSuccess(t *testing.T) {
	if err := dupAdd(t, &dupFakeQBit{addStatus: http.StatusOK, addBody: "Fails.", present: true}); err != nil {
		t.Fatalf(`duplicate 200 "Fails." with infohash present should be an idempotent success, got: %v`, err)
	}
}

// TestAdd_Reject409_NotPresent_ReturnsError guards that the fix does not mask a
// genuine failure: a 409 with the infohash absent must remain an error.
func TestAdd_Reject409_NotPresent_ReturnsError(t *testing.T) {
	if err := dupAdd(t, &dupFakeQBit{addStatus: http.StatusConflict, addBody: "Conflict", present: false}); err == nil {
		t.Fatal("409 with infohash absent should remain an error")
	}
}

// TestAdd_RejectFailsBody_NotPresent_ReturnsError guards that a genuine bad
// torrent (200 "Fails." with the infohash absent) still surfaces as an error.
func TestAdd_RejectFailsBody_NotPresent_ReturnsError(t *testing.T) {
	if err := dupAdd(t, &dupFakeQBit{addStatus: http.StatusOK, addBody: "Fails.", present: false}); err == nil {
		t.Fatal(`200 "Fails." with infohash absent should remain an error`)
	}
}
