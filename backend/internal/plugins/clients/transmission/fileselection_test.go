package transmission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

// newSelectServer answers the session-id dance, returns torrentsJSON for
// torrent-get, and records every call's method and arguments.
func newSelectServer(t *testing.T, torrentsJSON string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
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
		calls = append(calls, req)
		if req["method"] == "torrent-get" {
			w.Write([]byte(`{"result":"success","arguments":{"torrents":` + torrentsJSON + `}}`))
			return
		}
		w.Write([]byte(`{"result":"success","arguments":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFiles_ReadsNamesAndWanted(t *testing.T) {
	srv, _ := newSelectServer(t, `[{"files":[{"name":"Show/E01.mkv","length":100},{"name":"Show/E02.mkv","length":200}],"fileStats":[{"wanted":true},{"wanted":false}]}]`)
	got, err := newPlugin().Files(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
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

func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	srv, _ := newSelectServer(t, `[]`)
	got, err := newPlugin().Files(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

func TestSkipFiles_SendsFilesUnwanted(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().SkipFiles(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc", []int{0, 2}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	args := last["arguments"].(map[string]any)
	if last["method"] != "torrent-set" || !reflect.DeepEqual(args["files-unwanted"], []any{0.0, 2.0}) || !reflect.DeepEqual(args["ids"], []any{"abc"}) {
		t.Errorf("call = %v, want torrent-set ids [abc] files-unwanted [0 2]", last)
	}
}

func TestStart_SendsTorrentStart(t *testing.T) {
	srv, calls := newSelectServer(t, `[]`)
	if err := newPlugin().Start(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	if last["method"] != "torrent-start" {
		t.Errorf("method = %v, want torrent-start", last["method"])
	}
}

// A body cut short must fail, even when what did arrive is valid JSON: a
// truncated file list would pair with the torrent wrongly or not at all.
func TestFiles_TruncatedBodyIsAnError(t *testing.T) {
	const sessionID = "sess-1"
	const body = `{"result":"success","arguments":{"torrents":[{"files":[{"name":"Show/E01.mkv","length":100}],"fileStats":[{"wanted":true}]}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		// Promise more bytes than are sent; the connection then closes early.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+64))
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	got, err := newPlugin().Files(context.Background(), []byte(`{"url":"`+srv.URL+`"}`), "abc")
	if err == nil {
		t.Fatalf("Files = %+v, want an error for a truncated body", got)
	}
	if !strings.Contains(err.Error(), "read rpc response") {
		t.Errorf("err = %v, want it to say the response could not be read", err)
	}
}
