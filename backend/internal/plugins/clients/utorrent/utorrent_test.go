package utorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newServer(t *testing.T) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var addURLCalls, addFileCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/gui/token.html"):
			w.WriteHeader(200)
			w.Write([]byte(`<html><div id='token' style='display:none'>token-abc-123</div></html>`))
		case r.URL.Path == "/gui/" || r.URL.Path == "/gui":
			action := r.URL.Query().Get("action")
			switch action {
			case "add-url":
				atomic.AddInt32(&addURLCalls, 1)
				w.WriteHeader(200)
			case "add-file":
				atomic.AddInt32(&addFileCalls, 1)
				w.WriteHeader(200)
			default:
				w.WriteHeader(200)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &addURLCalls, &addFileCalls
}

func TestTestSucceeds(t *testing.T) {
	srv, _, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestTestRejectsBadCreds(t *testing.T) {
	srv, _, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "wrong"})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestAddMagnet(t *testing.T) {
	srv, addURLCalls, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if atomic.LoadInt32(addURLCalls) != 1 {
		t.Errorf("addURLCalls = %d", *addURLCalls)
	}
}

func TestAddTorrentFile(t *testing.T) {
	srv, _, addFileCalls := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	payload := &domain.Payload{TorrentFile: []byte("d8:announce..."), FileName: "x.torrent"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if atomic.LoadInt32(addFileCalls) != 1 {
		t.Errorf("addFileCalls = %d", *addFileCalls)
	}
}
