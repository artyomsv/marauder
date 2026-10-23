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

func TestBytesDone_ReadsCompleted(t *testing.T) {
	srv := &renameServer{infoBody: `[{"hash":"abc","completed":4096,"downloaded":0}]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	got, err := newRemovePlugin().BytesDone(context.Background(), selectCfg(ts.URL), "abc")
	if err != nil || got != 4096 {
		t.Errorf("BytesDone = (%d, %v), want (4096, nil)", got, err)
	}
	if srv.infoQuery != "abc" {
		t.Errorf("info hashes = %q, want abc", srv.infoQuery)
	}
}

func TestBytesDone_UnknownTorrentIsAnError(t *testing.T) {
	srv := &renameServer{infoBody: `[]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if _, err := newRemovePlugin().BytesDone(context.Background(), selectCfg(ts.URL), "abc"); err == nil {
		t.Error("BytesDone for an unknown torrent = nil error, want one")
	}
}
