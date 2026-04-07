package nnmclub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const fixtureViewtopicHTML = `<html><head><title>Big Anime Series :: NNM-Club</title></head>
<body>
<a href="logout.php">logout</a>
<div>Info-Hash: 0123456789ABCDEF0123456789ABCDEF01234567</div>
<a href="download.php?id=12345">скачать</a>
</body></html>`

func newTestPlugin(t *testing.T) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/forum/login.php"):
			http.SetCookie(w, &http.Cookie{Name: "phpbb2mysql_4_data", Value: "abc"})
			w.WriteHeader(200)
			w.Write([]byte(`<a href="logout.php">logout</a>`))
		case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureViewtopicHTML))
		case strings.HasPrefix(r.URL.Path, "/forum/download.php"):
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(200)
			w.Write([]byte("d8:announce..."))
		case r.URL.Path == "/forum/index.php":
			w.WriteHeader(200)
			w.Write([]byte(`<a href="logout.php">logout</a>`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:  forumcommon.New(),
		domain:    host,
		transport: &schemeRewrite{},
	}
}

type schemeRewrite struct{}

func (s *schemeRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req.URL.Scheme = "http"
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanParse(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://nnmclub.to/forum/viewtopic.php?t=12345":     true,
		"https://www.nnmclub.to/forum/viewtopic.php?t=12345": true,
		"https://nnmclub.me/forum/viewtopic.php?t=12345":     true,
		"https://nnmclub.to/forum/index.php":                 false,
		"https://example.com/":                               false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestUsesCloudflare(t *testing.T) {
	p := &plugin{}
	if !p.UsesCloudflare() {
		t.Error("nnm-club should report UsesCloudflare()")
	}
}

func TestParse(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://nnmclub.to/forum/viewtopic.php?t=42")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 42 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestCheck(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=42"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "Big Anime Series") {
		t.Errorf("display name: %q", check.DisplayName)
	}
}

func TestDownload(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=42"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Error("expected torrent bytes")
	}
}
