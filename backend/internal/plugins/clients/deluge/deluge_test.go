package deluge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var addCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		method, _ := req["method"].(string)
		switch method {
		case "auth.login":
			params, _ := req["params"].([]any)
			if len(params) == 0 || params[0] != "secret" {
				w.WriteHeader(200)
				w.Write([]byte(`{"id":1,"result":false,"error":null}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc"})
			w.WriteHeader(200)
			w.Write([]byte(`{"id":1,"result":true,"error":null}`))
		case "web.connected":
			w.WriteHeader(200)
			w.Write([]byte(`{"id":2,"result":true,"error":null}`))
		case "core.add_torrent_magnet":
			atomic.AddInt32(&addCalls, 1)
			w.WriteHeader(200)
			w.Write([]byte(`{"id":3,"result":"abc123hash","error":null}`))
		case "core.add_torrent_file":
			atomic.AddInt32(&addCalls, 1)
			w.WriteHeader(200)
			w.Write([]byte(`{"id":3,"result":"abc123hash","error":null}`))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &addCalls
}

func TestTestSucceeds(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Test(context.Background(), cfg); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestTestRejectsBadPassword(t *testing.T) {
	srv, _ := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "wrong"})
	if err := p.Test(context.Background(), cfg); err == nil {
		t.Fatal("expected login failure")
	}
}

func TestAddMagnet(t *testing.T) {
	srv, addCalls := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	payload := &domain.Payload{MagnetURI: "magnet:?xt=urn:btih:abc"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if atomic.LoadInt32(addCalls) != 1 {
		t.Errorf("addCalls = %d", *addCalls)
	}
}

func TestAddTorrentFile(t *testing.T) {
	srv, addCalls := newServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	payload := &domain.Payload{TorrentFile: []byte("d8:announce..."), FileName: "x.torrent"}
	if err := p.Add(context.Background(), cfg, payload, domain.AddOptions{DownloadDir: "/downloads"}); err != nil {
		t.Fatalf("Add file: %v", err)
	}
	if atomic.LoadInt32(addCalls) != 1 {
		t.Errorf("addCalls = %d", *addCalls)
	}
}
