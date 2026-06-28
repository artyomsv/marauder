package utorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRemoveServer(t *testing.T) (*httptest.Server, *struct {
	action string
	hashes []string
}) {
	t.Helper()
	rec := &struct {
		action string
		hashes []string
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(401)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/gui/token.html") {
			w.Write([]byte(`<div id='token'>token-abc</div>`))
			return
		}
		q := r.URL.Query()
		action := q.Get("action")
		if action == "remove" || action == "removedata" {
			rec.action = action
			rec.hashes = q["hash"]
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestRemove_DeleteData_UsesRemovedataAndUppercaseHashes(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"abc", "def"}, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if rec.action != "removedata" {
		t.Errorf("action = %q, want removedata", rec.action)
	}
	if len(rec.hashes) != 2 || rec.hashes[0] != "ABC" || rec.hashes[1] != "DEF" {
		t.Errorf("hashes = %v, want uppercase [ABC DEF]", rec.hashes)
	}
}

func TestRemove_KeepData_UsesRemove(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"abc"}, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if rec.action != "remove" {
		t.Errorf("action = %q, want remove", rec.action)
	}
}

func TestRemove_Non200_IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(401)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/gui/token.html") {
			w.Write([]byte(`<div id='token'>token-abc</div>`))
			return
		}
		// The remove action fails server-side.
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := p.Remove(context.Background(), cfg, []string{"abc"}, true); err == nil {
		t.Fatal("expected an error on a non-200 remove response")
	}
}

func TestRemove_Empty_NoCall(t *testing.T) {
	srv, rec := newRemoveServer(t)
	p := &plugin{sessions: map[string]*session{}}
	cfg, _ := json.Marshal(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := p.Remove(context.Background(), cfg, nil, true); err != nil {
		t.Fatalf("Remove(empty): %v", err)
	}
	if rec.action != "" {
		t.Errorf("action = %q, want no remove call", rec.action)
	}
}
