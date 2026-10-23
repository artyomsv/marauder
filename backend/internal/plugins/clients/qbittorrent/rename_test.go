package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithRename = (*plugin)(nil)

type renameServer struct {
	mu          sync.Mutex
	renameForm  map[string]string
	renameCode  int // 0 => 200
	recheckHash string
	infoQuery   string
	infoBody    string
	stopCode    int // 0 => 200
	stopHash    string
	pauseHash   string
}

func (s *renameServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("Ok.")) })
	mux.HandleFunc("/api/v2/torrents/renameFolder", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.renameForm = map[string]string{"hash": r.Form.Get("hash"), "oldPath": r.Form.Get("oldPath"), "newPath": r.Form.Get("newPath")}
		if s.renameCode != 0 {
			w.WriteHeader(s.renameCode)
			w.Write([]byte("New path is in use"))
		}
	})
	mux.HandleFunc("/api/v2/torrents/recheck", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.recheckHash = r.Form.Get("hashes")
		s.mu.Unlock()
	})
	mux.HandleFunc("/api/v2/torrents/stop", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.stopHash = r.Form.Get("hashes")
		if s.stopCode != 0 {
			w.WriteHeader(s.stopCode)
		}
	})
	mux.HandleFunc("/api/v2/torrents/pause", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.pauseHash = r.Form.Get("hashes")
		s.mu.Unlock()
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.infoQuery = r.URL.Query().Get("hashes")
		body := s.infoBody
		s.mu.Unlock()
		w.Write([]byte(body))
	})
	return mux
}

func TestRenameRoot_PostsRenameFolder(t *testing.T) {
	srv := &renameServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().RenameRoot(context.Background(), selectCfg(ts.URL), "abc", "Old", "New"); err != nil {
		t.Fatalf("RenameRoot: %v", err)
	}
	want := map[string]string{"hash": "abc", "oldPath": "Old", "newPath": "New"}
	for k, v := range want {
		if srv.renameForm[k] != v {
			t.Errorf("form %s = %q, want %q", k, srv.renameForm[k], v)
		}
	}
}

func TestRenameRoot_ConflictIsAnError(t *testing.T) {
	srv := &renameServer{renameCode: http.StatusConflict}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	err := newRemovePlugin().RenameRoot(context.Background(), selectCfg(ts.URL), "abc", "Old", "New")
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("RenameRoot error = %v, want one naming status 409", err)
	}
}

func TestRecheck_PostsHash(t *testing.T) {
	srv := &renameServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().Recheck(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
		t.Fatalf("Recheck: %v", err)
	}
	if srv.recheckHash != "abc" {
		t.Errorf("recheck hashes = %q, want abc", srv.recheckHash)
	}
}

// DiskState reads "completed" (bytes on disk) and whether the torrent is
// stopped: "paused*" before qBittorrent 5.0, "stopped*" since.
func TestDiskState_ReadsCompletedAndState(t *testing.T) {
	cases := []struct {
		state       string
		wantStopped bool
	}{
		{"stoppedDL", true}, {"pausedDL", true}, {"stoppedUP", true},
		{"downloading", false}, {"checkingDL", false}, {"queuedDL", false},
	}
	for _, tc := range cases {
		srv := &renameServer{infoBody: `[{"hash":"abc","completed":4096,"downloaded":0,"state":"` + tc.state + `"}]`}
		ts := httptest.NewServer(srv.handler())
		got, err := newRemovePlugin().DiskState(context.Background(), selectCfg(ts.URL), "abc")
		ts.Close()
		want := registry.DiskState{BytesOnDisk: 4096, Stopped: tc.wantStopped}
		if err != nil || got != want {
			t.Errorf("state %s: DiskState = (%+v, %v), want (%+v, nil)", tc.state, got, err, want)
		}
		if srv.infoQuery != "abc" {
			t.Errorf("info hashes = %q, want abc", srv.infoQuery)
		}
	}
}

// qBittorrent 5.0 renamed torrents/pause to torrents/stop; a 404 from stop
// means an older server.
func TestStop_FallsBackToPause(t *testing.T) {
	for _, code := range []int{0, http.StatusNotFound} {
		srv := &renameServer{stopCode: code}
		ts := httptest.NewServer(srv.handler())
		if err := newRemovePlugin().Stop(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
			t.Fatalf("Stop (stop status %d): %v", code, err)
		}
		ts.Close()
		if srv.stopHash != "abc" {
			t.Errorf("stop hashes = %q, want abc", srv.stopHash)
		}
		if wantPause := code == http.StatusNotFound; (srv.pauseHash == "abc") != wantPause {
			t.Errorf("stop status %d: pause hashes = %q, want pause called: %v", code, srv.pauseHash, wantPause)
		}
	}
}

func TestDiskState_UnknownTorrentIsAnError(t *testing.T) {
	srv := &renameServer{infoBody: `[]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if _, err := newRemovePlugin().DiskState(context.Background(), selectCfg(ts.URL), "abc"); err == nil {
		t.Error("DiskState for an unknown torrent = nil error, want one")
	}
}
