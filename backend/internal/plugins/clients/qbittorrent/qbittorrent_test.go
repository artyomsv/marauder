package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// fakeQBit is a tiny stand-in for the qBittorrent WebUI API v2.
type fakeQBit struct {
	loginCalls int
	addCalls   int
	lastBody   string
	mu         sync.Mutex
}

func (f *fakeQBit) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.loginCalls++
		f.mu.Unlock()
		_ = r.ParseForm()
		if r.Form.Get("username") == "admin" && r.Form.Get("password") == "secret" {
			w.WriteHeader(200)
			w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("Fails."))
	})
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("v4.6.0"))
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
		w.WriteHeader(200)
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

func TestTestSucceedsOnGoodCreds(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "secret",
	})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestTestFailsOnBadCreds(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}

	cfg, _ := json.Marshal(Config{
		URL: srv.URL, Username: "admin", Password: "wrong",
	})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected login failure")
	}
}

func TestAddMagnet(t *testing.T) {
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

func TestAddTorrentFile(t *testing.T) {
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

func TestSessionReuseAcrossCalls(t *testing.T) {
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

// nonUsedDeclarations stops the linter complaining about unused imports
// when this file is the entry point.
var _ = strings.Builder{}
var _ = multipart.NewWriter
