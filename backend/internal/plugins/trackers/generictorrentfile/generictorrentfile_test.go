package generictorrentfile

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newPlugin() *plugin {
	return &plugin{httpClient: &http.Client{Timeout: 5 * time.Second}}
}

func TestCanParse(t *testing.T) {
	p := newPlugin()
	cases := map[string]bool{
		"https://example.com/file.torrent":        true,
		"http://example.com/foo/bar.torrent":      true,
		"https://example.com/file.TORRENT":        true,
		"https://example.com/file.zip":            false,
		"magnet:?xt=urn:btih:abc":                 false,
		"ftp://example.com/file.torrent":          false,
		"":                                        false,
	}
	for url, want := range cases {
		if got := p.CanParse(url); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	p := newPlugin()
	topic, err := p.Parse(context.Background(), "https://example.com/movies/the-best-movie.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if topic.TrackerName != "generictorrentfile" {
		t.Errorf("tracker name: %s", topic.TrackerName)
	}
	if topic.DisplayName != "the-best-movie" {
		t.Errorf("display name: %s", topic.DisplayName)
	}
}

func TestCheckAndDownload(t *testing.T) {
	body := []byte("d8:announce26:http://tracker.example.com/4:infod6:lengthi42e4:name8:test.txt12:piece lengthi16384e6:pieces20:01234567890123456789ee")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		w.Write(body)
	}))
	defer srv.Close()

	p := newPlugin()
	url := srv.URL + "/test.torrent"

	// Check produces a SHA-1 of the file
	check, err := p.Check(context.Background(), &domain.Topic{URL: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantSum := sha1.Sum(body)
	wantHex := hex.EncodeToString(wantSum[:])
	if check.Hash != wantHex {
		t.Errorf("hash: got %s want %s", check.Hash, wantHex)
	}

	// Download returns the body bytes
	payload, err := p.Download(context.Background(), &domain.Topic{URL: url}, check, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.TorrentFile) != string(body) {
		t.Errorf("body mismatch")
	}
	if payload.FileName != "test.torrent" {
		t.Errorf("filename: %s", payload.FileName)
	}
}

func TestCheckRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	p := newPlugin()
	if _, err := p.Check(context.Background(), &domain.Topic{URL: srv.URL + "/x.torrent"}, nil); err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestHashChangesWhenBodyChanges(t *testing.T) {
	bodies := [][]byte{[]byte("v1"), []byte("v2"), []byte("v3")}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bodies[idx])
	}))
	defer srv.Close()

	p := newPlugin()
	url := srv.URL + "/x.torrent"

	hashes := make([]string, len(bodies))
	for i := range bodies {
		idx = i
		c, err := p.Check(context.Background(), &domain.Topic{URL: url}, nil)
		if err != nil {
			t.Fatal(err)
		}
		hashes[i] = c.Hash
	}
	for i := 0; i < len(hashes); i++ {
		for j := i + 1; j < len(hashes); j++ {
			if hashes[i] == hashes[j] {
				t.Errorf("hash[%d] == hash[%d] but bodies differ", i, j)
			}
		}
	}
}
