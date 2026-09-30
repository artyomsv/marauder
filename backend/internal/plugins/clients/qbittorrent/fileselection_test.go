package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

var _ registry.WithFileSelection = (*plugin)(nil)

type selectServer struct {
	mu         sync.Mutex
	filesBody  string // "" => 404 (torrent not known yet)
	prioForm   map[string]string
	startCode  int // 0 => 200
	startCalls int
	resumeHash string
}

func (s *selectServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("Ok.")) })
	mux.HandleFunc("/api/v2/torrents/files", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.filesBody == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(s.filesBody))
	})
	mux.HandleFunc("/api/v2/torrents/filePrio", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.prioForm = map[string]string{"hash": r.Form.Get("hash"), "id": r.Form.Get("id"), "priority": r.Form.Get("priority")}
		s.mu.Unlock()
	})
	mux.HandleFunc("/api/v2/torrents/start", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.startCalls++
		code := s.startCode
		s.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
		}
	})
	mux.HandleFunc("/api/v2/torrents/resume", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.resumeHash = r.Form.Get("hashes")
		s.mu.Unlock()
	})
	return mux
}

func selectCfg(url string) []byte {
	return []byte(`{"url":"` + url + `","username":"admin","password":"secret"}`)
}

func TestFiles_ReadsClientIndices(t *testing.T) {
	srv := &selectServer{filesBody: `[{"index":0,"name":"Show/E01.mkv","size":100,"priority":1},{"index":2,"name":"Show/E02.mkv","size":200,"priority":0}]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	got, err := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []domain.ClientFile{
		{Index: 0, Path: "Show/E01.mkv", Size: 100, Wanted: true},
		{Index: 2, Path: "Show/E02.mkv", Size: 200, Wanted: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %+v, want %+v", got, want)
	}
}

// qBittorrent answers 404 until an asynchronous add has been processed; that
// is "not yet", not an error, so the scheduler keeps polling.
func TestFiles_UnknownTorrentIsEmpty(t *testing.T) {
	ts := httptest.NewServer((&selectServer{}).handler())
	defer ts.Close()

	got, err := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if err != nil || len(got) != 0 {
		t.Errorf("Files = (%v, %v), want (empty, nil)", got, err)
	}
}

// Before qBittorrent 4.4 the file list has no index field; the position is the id.
func TestFiles_MissingIndexUsesPosition(t *testing.T) {
	srv := &selectServer{filesBody: `[{"name":"a","size":1,"priority":1},{"name":"b","size":2,"priority":1}]`}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	got, _ := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if len(got) != 2 || got[1].Index != 1 {
		t.Errorf("Files = %+v, want position indices", got)
	}
}

func TestSkipFiles_SetsPriorityZero(t *testing.T) {
	srv := &selectServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().SkipFiles(context.Background(), selectCfg(ts.URL), "abc", []int{0, 3}); err != nil {
		t.Fatalf("SkipFiles: %v", err)
	}
	want := map[string]string{"hash": "abc", "id": "0|3", "priority": "0"}
	if !reflect.DeepEqual(srv.prioForm, want) {
		t.Errorf("filePrio form = %v, want %v", srv.prioForm, want)
	}
}

func TestStart_FallsBackToResumeOn404(t *testing.T) {
	srv := &selectServer{startCode: http.StatusNotFound}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().Start(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.resumeHash != "abc" {
		t.Errorf("resume hashes = %q, want abc (qBittorrent 4.x fallback)", srv.resumeHash)
	}
}

func TestStart_UsesStartOn5x(t *testing.T) {
	srv := &selectServer{}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	if err := newRemovePlugin().Start(context.Background(), selectCfg(ts.URL), "abc"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.startCalls != 1 || srv.resumeHash != "" {
		t.Errorf("start calls = %d, resume = %q; want start only", srv.startCalls, srv.resumeHash)
	}
}

// A body cut short must fail, even when what did arrive is valid JSON: a
// truncated file list would pair with the torrent wrongly or not at all.
func TestFiles_TruncatedBodyIsAnError(t *testing.T) {
	const body = `[{"index":0,"name":"Show/E01.mkv","size":100,"priority":1}]`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("Ok.")) })
	mux.HandleFunc("/api/v2/torrents/files", func(w http.ResponseWriter, _ *http.Request) {
		// Promise more bytes than are sent; the connection then closes early.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+64))
		w.Write([]byte(body))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	got, err := newRemovePlugin().Files(context.Background(), selectCfg(ts.URL), "abc")
	if err == nil {
		t.Fatalf("Files = %+v, want an error for a truncated body", got)
	}
	if !strings.Contains(err.Error(), "read files") {
		t.Errorf("err = %v, want it to say the file list could not be read", err)
	}
}
