package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeQBit is a tiny stand-in for the qBittorrent WebUI API v2.
type fakeQBit struct {
	loginCalls int
	addCalls   int
	lastBody   string
	// login204 mimics qBittorrent >=5.2.x, which answers a successful login
	// with 204 No Content and an empty body instead of 200 "Ok.".
	login204 bool
	// addJSON mimics qBittorrent's newer /torrents/add, which answers with a
	// JSON summary ({"success_count":1,"failure_count":0,...}) instead of the
	// legacy "Ok." string. The JSON contains the substring "fail" in
	// "failure_count", which must NOT be read as a rejection.
	addJSON bool
	mu      sync.Mutex
}

func (f *fakeQBit) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.loginCalls++
		use204 := f.login204
		f.mu.Unlock()
		_ = r.ParseForm()
		if r.Form.Get("username") == "admin" && r.Form.Get("password") == "secret" {
			if use204 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		// Bad credentials are rejected the same way in both contracts:
		// 200 with a "Fails." body. The login204 flag only governs success.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Fails."))
	})
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastBody = r.URL.Query().Get("hashes")
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		// One downloading, one finished-seeding torrent.
		w.Write([]byte(`[` +
			`{"hash":"ABC123","progress":0.42,"state":"downloading"},` +
			`{"hash":"def456","progress":1,"state":"stalledUP"}]`))
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.addCalls++
		// Drain multipart so we can verify the urls field
		mr, err := r.MultipartReader()
		if err == nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				if p.FormName() == "urls" {
					b, _ := io.ReadAll(p)
					f.lastBody = string(b)
				}
			}
		}
		// f.mu is already held for the duration of this handler (deferred
		// unlock at the top), so read addJSON directly — do not re-lock.
		useJSON := f.addJSON
		w.WriteHeader(http.StatusOK)
		if useJSON {
			w.Write([]byte(`{"added_torrent_ids":["abc"],"failure_count":0,"pending_count":0,"success_count":1}`))
			return
		}
		w.Write([]byte("Ok."))
	})
	return mux
}

func newServer(t *testing.T) (*httptest.Server, *fakeQBit) {
	t.Helper()
	f := &fakeQBit{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv, f
}

func TestTest_ValidCredentials_Succeeds(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "secret",
	})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestTest_InvalidCredentials_ReturnsError(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "wrong",
	})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected login failure")
	}
}

// TestTest_204LoginResponse_Succeeds is the regression test for issue #38:
// qBittorrent 5.2.x answers a successful login with 204 No Content (empty
// body). The whole Test() flow — which goes through session() — must accept it.
func TestTest_204LoginResponse_Succeeds(t *testing.T) {
	srv, fake := newServer(t)
	fake.login204 = true
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "secret",
	})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test against a 204-login server: %v", err)
	}
}

// TestTest_InvalidCredentialsOn204Server_ReturnsError guards that the 204
// success contract does not turn a rejected login into a false success: a
// 5.2.x-style server still answers bad credentials with 200 "Fails.".
func TestTest_InvalidCredentialsOn204Server_ReturnsError(t *testing.T) {
	srv, fake := newServer(t)
	fake.login204 = true
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "wrong",
	})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected login failure on a 204-mode server with bad creds")
	}
}

func TestAdd_MagnetURI_SubmitsURLField(t *testing.T) {
	srv, fake := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.addCalls != 1 {
		t.Errorf("addCalls = %d", fake.addCalls)
	}
	if fake.lastBody != payload.MagnetURI {
		t.Errorf("urls form field = %q", fake.lastBody)
	}
}

// TestAdd_JSONSuccessResponse_NotRejected guards against a regression where
// newer qBittorrent answers /torrents/add with a JSON summary whose
// "failure_count" field contains the substring "fail". A naive substring
// check turned that success into a false "qbittorrent rejected torrent"
// error, so deliveries were never recorded and the scheduler retried forever.
func TestAdd_JSONSuccessResponse_NotRejected(t *testing.T) {
	srv, fake := newServer(t)
	fake.addJSON = true
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add against a JSON-summary server: %v", err)
	}
}

func TestAdd_TorrentFile_Succeeds(t *testing.T) {
	srv, fake := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{
		TorrentFile: []byte("d8:announce..."),
		FileName:    "movie.torrent",
	}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.addCalls != 1 {
		t.Errorf("addCalls = %d", fake.addCalls)
	}
}

func TestSession_RepeatedCalls_LogsInOnce(t *testing.T) {
	srv, fake := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}

	for i := 0; i < 3; i++ {
		if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	// Should login once and reuse the session.
	if fake.loginCalls != 1 {
		t.Errorf("loginCalls = %d, want 1", fake.loginCalls)
	}
	if fake.addCalls != 3 {
		t.Errorf("addCalls = %d, want 3", fake.addCalls)
	}
}

func TestStatus_MultipleHashes_MapsStatesAndProgress(t *testing.T) {
	srv, fake := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})

	got, err := p.Status(context.Background(), cfg, []string{"abc123", "def456"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2", len(got))
	}
	// The plugin pipe-joins requested hashes into the hashes= filter.
	if fake.lastBody != "abc123|def456" {
		t.Errorf("hashes filter = %q, want abc123|def456", fake.lastBody)
	}
	byHash := map[string]registry.TorrentStatus{}
	for _, s := range got {
		byHash[s.Hash] = s
	}
	if s := byHash["abc123"]; s.State != registry.StateDownloading || s.PercentDone != 0.42 {
		t.Errorf("abc123 = %+v, want downloading @ 0.42 (lower-cased hash)", s)
	}
	if s := byHash["def456"]; s.State != registry.StateSeeding || s.PercentDone != 1 {
		t.Errorf("def456 = %+v, want seeding @ 1", s)
	}
}

// TestLoginSucceeded covers both qBittorrent WebUI login response contracts:
//   - legacy (<=5.1.x): 200 OK with body "Ok." on success, "Fails." on bad creds
//   - current (>=5.2.x): 204 No Content with an empty body on success
//
// Verified empirically against linuxserver/qbittorrent 5.1.4 (200 "Ok.") and
// 5.2.1 (204, empty) — see issue #38.
func TestLoginSucceeded(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"legacy 200 Ok.", 200, "Ok.", true},
		{"legacy 200 Ok. with trailing newline", 200, "Ok.\n", true},
		{"legacy 200 case-insensitive ok.", 200, "OK.", true},
		{"current 204 empty body", 204, "", true},
		{"current 204 with whitespace body", 204, "  \n", true},
		{"bad creds 200 Fails.", 200, "Fails.", false},
		{"200 empty body", 200, "", false},
		{"204 with unexpected non-empty body", 204, "nope", false},
		{"403 banned empty body", 403, "", false},
		{"500 server error", 500, "Ok.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loginSucceeded(tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("loginSucceeded(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
			}
		})
	}
}
