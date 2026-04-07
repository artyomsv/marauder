// Package e2etest provides shared helpers for plugin E2E tests.
//
// It contains a stand-in qBittorrent WebUI v2 server that captures
// every torrent submission, and a runner that drives a tracker plugin
// through its complete pipeline: Parse -> (Login -> Verify) ->
// Check -> Download -> submit-to-fake-qBit -> assert.
//
// E2E tests live alongside each tracker as `<name>_e2e_test.go` files
// inside the plugin's own package (so they can construct the plugin
// directly with package-private fields).
package e2etest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// SubmittedTorrent captures one torrent that was handed to the fake
// qBittorrent.
type SubmittedTorrent struct {
	// Magnet is the magnet URI submitted, if any.
	Magnet string

	// TorrentFile is the raw .torrent bytes submitted, if any.
	TorrentFile []byte

	// FileName is the multipart filename used for a .torrent submission.
	FileName string

	// Category is the qBittorrent "category" form field, if set.
	Category string

	// SavePath is the qBittorrent "savepath" form field, if set.
	SavePath string
}

// QBitFake is an httptest server that mimics the parts of the
// qBittorrent WebUI API v2 that the qbittorrent plugin actually calls:
// /api/v2/auth/login, /api/v2/app/version, /api/v2/torrents/add.
//
// Calls are recorded for later assertions.
type QBitFake struct {
	Server   *httptest.Server
	URL      string
	Username string
	Password string

	mu        sync.Mutex
	submitted []SubmittedTorrent
	addCalls  int32
	loginOK   int32
}

// NewQBitFake constructs a fresh fake server registered for cleanup
// at the end of the test. Default credentials are admin / qbitsecret.
func NewQBitFake(t *testing.T) *QBitFake {
	t.Helper()
	f := &QBitFake{
		Username: "admin",
		Password: "qbitsecret",
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		// Bound the form size — gosec G120 wants this even on a test
		// fake server. 64 KiB is plenty for a username + password.
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		_ = r.ParseForm()
		if r.Form.Get("username") == f.Username && r.Form.Get("password") == f.Password {
			atomic.AddInt32(&f.loginOK, 1)
			w.WriteHeader(200)
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("Fails."))
	})

	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("v4.6.0"))
	})

	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		atomic.AddInt32(&f.addCalls, 1)

		entry := SubmittedTorrent{}
		if mr, err := r.MultipartReader(); err == nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				switch p.FormName() {
				case "urls":
					b, _ := io.ReadAll(p)
					entry.Magnet = strings.TrimSpace(string(b))
				case "torrents":
					var buf bytes.Buffer
					_, _ = io.Copy(&buf, p)
					entry.TorrentFile = buf.Bytes()
					entry.FileName = p.FileName()
				case "category":
					b, _ := io.ReadAll(p)
					entry.Category = string(b)
				case "savepath":
					b, _ := io.ReadAll(p)
					entry.SavePath = string(b)
				default:
					_, _ = io.Copy(io.Discard, p)
				}
			}
		}
		f.submitted = append(f.submitted, entry)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("Ok."))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.Server = srv
	f.URL = srv.URL
	return f
}

// AddCalls returns the number of /api/v2/torrents/add requests received.
func (f *QBitFake) AddCalls() int32 { return atomic.LoadInt32(&f.addCalls) }

// LoginCalls returns the number of successful logins.
func (f *QBitFake) LoginCalls() int32 { return atomic.LoadInt32(&f.loginOK) }

// Submitted returns a snapshot of every torrent the fake has received.
func (f *QBitFake) Submitted() []SubmittedTorrent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SubmittedTorrent, len(f.submitted))
	copy(out, f.submitted)
	return out
}

// Last returns the most recent submission, or zero value if none.
func (f *QBitFake) Last() SubmittedTorrent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.submitted) == 0 {
		return SubmittedTorrent{}
	}
	return f.submitted[len(f.submitted)-1]
}
