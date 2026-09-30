package deluge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

func newSelectServer(t *testing.T, statusJSON string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		calls = append(calls, req)
		switch req["method"] {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc"})
			w.Write([]byte(`{"id":1,"result":true,"error":null}`))
		case "web.connected":
			w.Write([]byte(`{"id":2,"result":true,"error":null}`))
		case "core.get_torrent_status":
			w.Write([]byte(`{"id":3,"result":` + statusJSON + `,"error":null}`))
		default:
			w.Write([]byte(`{"id":4,"result":null,"error":null}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func delugeCfg(url string) []byte {
	b, _ := json.Marshal(Config{URL: url, Password: "secret"})
	return b
}

func TestFiles_ReadsFilesAndPriorities(t *testing.T) {
	srv, _ := newSelectServer(t, `{"files":[{"index":0,"path":"Show/E01.mkv","size":100},{"index":1,"path":"Show/E02.mkv","size":200}],"file_priorities":[4,0]}`)
	p := &plugin{sessions: map[string]*session{}}
	got, err := p.Files(context.Background(), delugeCfg(srv.URL), "abc")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 1, Path: "Show/E02.mkv", Size: 200, Wanted: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// Deluge answers an unknown torrent id with an empty status dictionary.
func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	srv, _ := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	got, err := p.Files(context.Background(), delugeCfg(srv.URL), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

// Deluge takes the whole priority list, so SkipFiles must keep the current
// priority of every file it does not skip.
func TestSkipFiles_KeepsOtherPriorities(t *testing.T) {
	srv, calls := newSelectServer(t, `{"files":[{"index":0,"path":"a","size":1},{"index":1,"path":"b","size":2},{"index":2,"path":"c","size":3}],"file_priorities":[4,7,4]}`)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.SkipFiles(context.Background(), delugeCfg(srv.URL), "abc", []int{0, 2}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	params := last["params"].([]any)
	opts := params[1].(map[string]any)
	if last["method"] != "core.set_torrent_options" || !reflect.DeepEqual(params[0], []any{"abc"}) ||
		!reflect.DeepEqual(opts["file_priorities"], []any{0.0, 7.0, 0.0}) {
		t.Errorf("call = %v, want set_torrent_options([abc], {file_priorities: [0 7 0]})", last)
	}
}

func TestStart_ResumesTorrent(t *testing.T) {
	srv, calls := newSelectServer(t, `{}`)
	p := &plugin{sessions: map[string]*session{}}
	if err := p.Start(context.Background(), delugeCfg(srv.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	if last["method"] != "core.resume_torrent" || !reflect.DeepEqual(last["params"], []any{"abc"}) {
		t.Errorf("call = %v, want core.resume_torrent(abc)", last)
	}
}
