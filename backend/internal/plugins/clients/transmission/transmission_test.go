package transmission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeServer struct {
	sessionID    string
	addCalls     int32
	requireAuth  bool
	lastDownload string
}

func newFakeServer(t *testing.T) (*httptest.Server, *fakeServer) {
	t.Helper()
	f := &fakeServer{sessionID: "abc-session-123"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.requireAuth {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "rpcuser" || pass != "rpcpass" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		incoming := r.Header.Get("X-Transmission-Session-Id")
		if incoming != f.sessionID {
			w.Header().Set("X-Transmission-Session-Id", f.sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch req["method"] {
		case "session-get":
			w.WriteHeader(200)
			w.Write([]byte(`{"result":"success","arguments":{"version":"4.0.6"}}`))
		case "torrent-add":
			atomic.AddInt32(&f.addCalls, 1)
			if args, ok := req["arguments"].(map[string]any); ok {
				f.lastDownload, _ = args["download-dir"].(string)
			}
			w.WriteHeader(200)
			w.Write([]byte(`{"result":"success","arguments":{"torrent-added":{"id":1,"hashString":"abc"}}}`))
		case "torrent-get":
			// Mixed-case hashString to prove the plugin lower-cases it; one
			// downloading (status 4) and one seeding (status 6).
			w.WriteHeader(200)
			w.Write([]byte(`{"result":"success","arguments":{"torrents":[` +
				`{"hashString":"ABC123","percentDone":0.5,"status":4},` +
				`{"hashString":"def456","percentDone":1,"status":6}]}}`))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, f
}

func newPlugin() *plugin {
	return &plugin{sessions: map[string]string{}, client: http.DefaultClient}
}

func TestTestSucceeds(t *testing.T) {
	srv, _ := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestTestWithAuth(t *testing.T) {
	srv, fake := newFakeServer(t)
	fake.requireAuth = true
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "rpcuser", Password: "rpcpass"})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test with auth: %v", err)
	}
}

func TestTestRejectsBadAuth(t *testing.T) {
	srv, fake := newFakeServer(t)
	fake.requireAuth = true
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "rpcuser", Password: "wrong"})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestAddMagnet(t *testing.T) {
	srv, fake := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if atomic.LoadInt32(&fake.addCalls) != 1 {
		t.Errorf("addCalls = %d", fake.addCalls)
	}
}

func TestAddTorrentFile(t *testing.T) {
	srv, fake := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	payload := &domain.Payload{TorrentFile: []byte("d8:announce..."), FileName: "x.torrent"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{DownloadDir: "/downloads"}); err != nil {
		t.Fatalf("Add file: %v", err)
	}
	if atomic.LoadInt32(&fake.addCalls) != 1 {
		t.Errorf("addCalls = %d", fake.addCalls)
	}
}

func TestAddNestsCategoryUnderBaseDownloadDir(t *testing.T) {
	srv, fake := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL, DownloadDir: "/downloads"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{Category: "Movies"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastDownload != "/downloads/Movies" {
		t.Errorf("download-dir = %q, want /downloads/Movies", fake.lastDownload)
	}
}

func TestAddExplicitDirOverridesBase(t *testing.T) {
	srv, fake := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL, DownloadDir: "/downloads"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{DownloadDir: "/explicit", Category: "Movies"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.lastDownload != "/explicit" {
		t.Errorf("download-dir = %q, want /explicit", fake.lastDownload)
	}
}

func TestStatusMapsHashesAndStates(t *testing.T) {
	srv, _ := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	got, err := p.Status(context.Background(), cfg, []string{"abc123", "def456"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2", len(got))
	}
	byHash := map[string]registry.TorrentStatus{}
	for _, s := range got {
		byHash[s.Hash] = s
	}
	if s := byHash["abc123"]; s.State != registry.StateDownloading || s.PercentDone != 0.5 {
		t.Errorf("abc123 = %+v, want downloading @ 0.5 (lower-cased hash)", s)
	}
	if s := byHash["def456"]; s.State != registry.StateSeeding || s.PercentDone != 1 {
		t.Errorf("def456 = %+v, want seeding @ 1", s)
	}
}

func TestStatusEmptyHashesNoCall(t *testing.T) {
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: "http://unused.invalid"})
	got, err := p.Status(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Status(nil): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for no hashes, got %+v", got)
	}
}

func TestSessionIDIsCachedAfterFirstRoundTrip(t *testing.T) {
	srv, fake := newFakeServer(t)
	p := newPlugin()
	cfg, _ := json.Marshal(Config{URL: srv.URL})
	for i := 0; i < 3; i++ {
		if err := p.Test(context.Background(), cfg); err != nil {
			t.Fatalf("Test %d: %v", i, err)
		}
	}
	// First Test triggered the 409 dance; subsequent calls reuse the cached id.
	// We don't directly observe the count of 409s, but we verified
	// p.sessions has been populated.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions[srv.URL] != fake.sessionID {
		t.Errorf("session id not cached: %q", p.sessions[srv.URL])
	}
}
