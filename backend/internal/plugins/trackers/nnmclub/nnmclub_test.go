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

const fixtureViewtopicHTML = `<html><head>
<title>Через тернии к звёздам (1980) DVDRip [H.264] Оригинальная версия :: NNM-Club</title>
<meta property="og:image" content="https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg"/>
</head>
<body>
<a href="logout.php">logout</a>
<a rel="nofollow" href="magnet:?xt=urn:btih:094EC3052ED759240E4DFD89F3F7CA5C5B428FF4" title="Примагнититься"><img src="https://nnmstatic.win/forum/images/magnet.png"></a>
<a href="download.php?id=379398" rel="nofollow">Скачать</a>
</body></html>`

func newTestPlugin(t *testing.T) *plugin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
			w.WriteHeader(200)
			w.Write([]byte(fixtureViewtopicHTML))
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
	if check.Hash != "094ec3052ed759240e4dfd89f3f7ca5c5b428ff4" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "Через тернии к звёздам") {
		t.Errorf("display name: %q", check.DisplayName)
	}
	if strings.HasSuffix(check.DisplayName, " :: NNM-Club") {
		t.Errorf("site suffix not stripped: %q", check.DisplayName)
	}
}

func TestDownload(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=42"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Errorf("expected no torrent file in anonymous mode, got %d bytes", len(payload.TorrentFile))
	}
	if !strings.Contains(payload.MagnetURI, "urn:btih:094ec3052ed759240e4dfd89f3f7ca5c5b428ff4") {
		t.Errorf("magnet missing infohash: %q", payload.MagnetURI)
	}
	if !strings.Contains(payload.MagnetURI, "dn=") {
		t.Errorf("magnet missing display name: %q", payload.MagnetURI)
	}
}

func TestResolveMetadata(t *testing.T) {
	p := newTestPlugin(t)
	meta, err := p.ResolveMetadata(context.Background(), "https://"+p.domain+"/forum/viewtopic.php?t=42", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if !strings.Contains(meta.Title, "Через тернии к звёздам") {
		t.Errorf("title: %q", meta.Title)
	}
	if meta.ImageURL != "https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg" {
		t.Errorf("image url: %q", meta.ImageURL)
	}
}
