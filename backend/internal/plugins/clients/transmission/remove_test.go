package transmission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newRemoveServer answers the session-id dance and records torrent-remove args.
func newRemoveServer(t *testing.T) (*httptest.Server, *struct {
	removeCalls int
	lastIDs     []any
	lastDelete  bool
}) {
	t.Helper()
	rec := &struct {
		removeCalls int
		lastIDs     []any
		lastDelete  bool
	}{}
	const sessionID = "sess-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["method"] == "torrent-remove" {
			rec.removeCalls++
			if args, ok := req["arguments"].(map[string]any); ok {
				rec.lastIDs, _ = args["ids"].([]any)
				rec.lastDelete, _ = args["delete-local-data"].(bool)
			}
			w.WriteHeader(200)
			w.Write([]byte(`{"result":"success","arguments":{}}`))
			return
		}
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestRemove_DeletesData(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := newPlugin()
	cfg := []byte(`{"url":"` + srv.URL + `"}`)
	if err := p.Remove(context.Background(), cfg, []string{"aaa", "bbb"}, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if rec.removeCalls != 1 {
		t.Fatalf("remove calls = %d, want 1", rec.removeCalls)
	}
	if len(rec.lastIDs) != 2 {
		t.Errorf("ids = %v, want 2 hashes", rec.lastIDs)
	}
	if !rec.lastDelete {
		t.Errorf("delete-local-data = false, want true")
	}
}

func TestRemove_KeepData(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := newPlugin()
	cfg := []byte(`{"url":"` + srv.URL + `"}`)
	if err := p.Remove(context.Background(), cfg, []string{"aaa"}, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if rec.lastDelete {
		t.Errorf("delete-local-data = true, want false")
	}
}

func TestRemove_RPCFailure_IsError(t *testing.T) {
	const sessionID = "sess-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		// torrent-remove answers with a non-success result.
		w.WriteHeader(200)
		w.Write([]byte(`{"result":"invalid argument","arguments":{}}`))
	}))
	t.Cleanup(srv.Close)
	p := newPlugin()
	cfg := []byte(`{"url":"` + srv.URL + `"}`)
	if err := p.Remove(context.Background(), cfg, []string{"aaa"}, true); err == nil {
		t.Fatal("expected an error when Transmission rejects the remove")
	}
}

func TestRemove_Empty_NoCall(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := newPlugin()
	cfg := []byte(`{"url":"` + srv.URL + `"}`)
	if err := p.Remove(context.Background(), cfg, nil, true); err != nil {
		t.Fatalf("Remove(empty): %v", err)
	}
	if rec.removeCalls != 0 {
		t.Errorf("remove calls = %d, want 0 for empty set", rec.removeCalls)
	}
}
