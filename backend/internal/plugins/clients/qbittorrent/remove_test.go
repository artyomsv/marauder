package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// removeServer is a minimal qBittorrent stand-in that records the
// /api/v2/torrents/delete form fields.
type removeServer struct {
	mu          sync.Mutex
	deleteCalls int
	lastHashes  string
	lastDelete  string
	status      int // override response status for /torrents/delete (0 => 200)
}

func (s *removeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.deleteCalls++
		s.lastHashes = r.Form.Get("hashes")
		s.lastDelete = r.Form.Get("deleteFiles")
		st := s.status
		s.mu.Unlock()
		if st != 0 {
			w.WriteHeader(st)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func newRemovePlugin() *plugin { return &plugin{sessions: map[string]*session{}} }

func TestRemove_DeletesFiles_JoinsHashes(t *testing.T) {
	srv := &removeServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	p := newRemovePlugin()
	cfg := []byte(`{"url":"` + ts.URL + `","username":"admin","password":"secret"}`)
	err := p.Remove(context.Background(), cfg, []string{"aaa", "bbb"}, true)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", srv.deleteCalls)
	}
	if srv.lastHashes != "aaa|bbb" {
		t.Errorf("hashes = %q, want pipe-joined", srv.lastHashes)
	}
	if srv.lastDelete != "true" {
		t.Errorf("deleteFiles = %q, want true", srv.lastDelete)
	}
}

func TestRemove_KeepData_SendsFalse(t *testing.T) {
	srv := &removeServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	p := newRemovePlugin()
	cfg := []byte(`{"url":"` + ts.URL + `","username":"admin","password":"secret"}`)
	if err := p.Remove(context.Background(), cfg, []string{"aaa"}, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.lastDelete != "false" {
		t.Errorf("deleteFiles = %q, want false", srv.lastDelete)
	}
}

func TestRemove_EmptyHashes_NoCall(t *testing.T) {
	srv := &removeServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	p := newRemovePlugin()
	cfg := []byte(`{"url":"` + ts.URL + `","username":"admin","password":"secret"}`)
	if err := p.Remove(context.Background(), cfg, nil, true); err != nil {
		t.Fatalf("Remove(empty): %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.deleteCalls != 0 {
		t.Errorf("delete calls = %d, want 0 for empty hash set", srv.deleteCalls)
	}
}

func TestRemove_Non200_IsError(t *testing.T) {
	srv := &removeServer{status: http.StatusForbidden}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	p := newRemovePlugin()
	cfg := []byte(`{"url":"` + ts.URL + `","username":"admin","password":"secret"}`)
	if err := p.Remove(context.Background(), cfg, []string{"aaa"}, true); err == nil {
		t.Fatal("expected an error on non-200 delete response")
	}
}
