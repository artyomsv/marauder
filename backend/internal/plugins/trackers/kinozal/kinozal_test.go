package kinozal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const fixtureDetailsHTML = `<html><head><title>The Movie / Кино (2026) [BDRip] [1080p] / Кинозал.ТВ</title></head>
<body>
<a href="/logout.php">Выход</a>
<div>Инфо хэш: 0123456789ABCDEF0123456789ABCDEF01234567</div>
</body></html>`

func newTestPlugin(t *testing.T) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/takelogin.php"):
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "42"})
			w.WriteHeader(200)
			w.Write([]byte(`<a href="/logout.php">Выход</a>`))
		case strings.HasPrefix(r.URL.Path, "/details.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureDetailsHTML))
		case strings.HasPrefix(r.URL.Path, "/download.php"):
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.WriteHeader(200)
			w.Write([]byte("d8:announce..."))
		case r.URL.Path == "/":
			w.WriteHeader(200)
			w.Write([]byte(`<a href="/logout.php">Выход</a>`))
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
	// dl.<host> -> <host>
	if strings.HasPrefix(req.URL.Host, "dl.") {
		req.URL.Host = strings.TrimPrefix(req.URL.Host, "dl.")
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanParse(t *testing.T) {
	p := &plugin{}
	cases := map[string]bool{
		"https://kinozal.tv/details.php?id=12345":     true,
		"https://www.kinozal.tv/details.php?id=12345": true,
		"https://kinozal.me/details.php?id=12345":     true,
		"https://kinozal.tv/userlist.php":             false,
		"":                                             false,
	}
	for u, want := range cases {
		if got := p.CanParse(u); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	p := &plugin{}
	topic, err := p.Parse(context.Background(), "https://kinozal.tv/details.php?id=99999")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Extra["topic_id"] != 99999 {
		t.Errorf("topic_id: %v", topic.Extra["topic_id"])
	}
}

func TestCheck(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/details.php?id=99999"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "The Movie") {
		t.Errorf("display name: %q", check.DisplayName)
	}
}

func TestDownload(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{
		URL:   "https://" + p.domain + "/details.php?id=99999",
		Extra: map[string]any{"topic_id": 99999},
	}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) == 0 {
		t.Error("expected torrent bytes")
	}
}
