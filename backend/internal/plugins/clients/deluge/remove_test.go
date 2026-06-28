package deluge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newRemoveServer(t *testing.T) (*httptest.Server, *struct {
	removeParams [][]any
}) {
	t.Helper()
	rec := &struct{ removeParams [][]any }{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch req["method"] {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc"})
			w.Write([]byte(`{"id":1,"result":true,"error":null}`))
		case "web.connected":
			w.Write([]byte(`{"id":2,"result":true,"error":null}`))
		case "core.remove_torrent":
			params, _ := req["params"].([]any)
			rec.removeParams = append(rec.removeParams, params)
			w.Write([]byte(`{"id":3,"result":true,"error":null}`))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestRemove_OneCallPerHash_WithDeleteFlag(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"hash1", "hash2"}, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(rec.removeParams) != 2 {
		t.Fatalf("remove calls = %d, want 2 (one per hash)", len(rec.removeParams))
	}
	// Each call is [torrent_id, remove_data].
	if rec.removeParams[0][0] != "hash1" {
		t.Errorf("first torrent_id = %v, want hash1", rec.removeParams[0][0])
	}
	if del, _ := rec.removeParams[0][1].(bool); !del {
		t.Errorf("remove_data = false, want true")
	}
}

func TestRemove_KeepData(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"hash1"}, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if del, _ := rec.removeParams[0][1].(bool); del {
		t.Errorf("remove_data = true, want false")
	}
}

func TestRemove_Empty_NoCall(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Remove(context.Background(), cfg, nil, true); err != nil {
		t.Fatalf("Remove(empty): %v", err)
	}
	if len(rec.removeParams) != 0 {
		t.Errorf("remove calls = %d, want 0 for empty set", len(rec.removeParams))
	}
}

// removeServerWithError answers core.remove_torrent with a JSON-RPC error
// whose message is supplied per-test, so we can simulate Deluge's
// InvalidTorrentError (unknown id) vs. a genuine failure.
func removeServerWithError(t *testing.T, rpcErr string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch req["method"] {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc"})
			w.Write([]byte(`{"id":1,"result":true,"error":null}`))
		case "web.connected":
			w.Write([]byte(`{"id":2,"result":true,"error":null}`))
		case "core.remove_torrent":
			w.Write([]byte(`{"id":3,"result":null,"error":{"message":"` + rpcErr + `"}}`))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRemove_UnknownTorrent_IsIdempotent(t *testing.T) {
	// Deluge raises InvalidTorrentError for a torrent_id it no longer knows.
	// Remove must treat that as success (the torrent is already gone).
	srv := removeServerWithError(t, "InvalidTorrentError: torrent not in session")
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"gonehash"}, true); err != nil {
		t.Fatalf("Remove(unknown torrent) should be a no-op, got %v", err)
	}
}

func TestRemove_GenuineRPCError_IsError(t *testing.T) {
	srv := removeServerWithError(t, "some internal deluge failure")
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"h1"}, true); err == nil {
		t.Fatal("expected an error on a genuine RPC failure")
	}
}
