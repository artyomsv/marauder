package torznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
)

func TestCanParse(t *testing.T) {
	p := New(nil)
	cases := map[string]bool{
		"torznab+https://example.com/api?apikey=K&t=search&q=foo": true,
		"torznab+http://localhost:9117/api":                       true,
		"https://example.com/api?apikey=K&t=search":               false,
		"magnet:?xt=urn:btih:abc":                                 false,
		"https://rutracker.org/forum/viewtopic.php?t=1":           false,
		"":                                                         false,
		"torznab+ftp://example.com/api":                           false,
	}
	for url, want := range cases {
		if got := p.CanParse(url); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParseExtractsIndexerURL(t *testing.T) {
	p := New(nil)
	topic, err := p.Parse(context.Background(), "torznab+https://prowlarr.example.com/api?apikey=K&t=search&q=Some+Show")
	if err != nil {
		t.Fatal(err)
	}
	if topic.TrackerName != "torznab" {
		t.Errorf("tracker name: %s", topic.TrackerName)
	}
	got, _ := topic.Extra["indexer_url"].(string)
	want := "https://prowlarr.example.com/api?apikey=K&t=search&q=Some+Show"
	if got != want {
		t.Errorf("indexer_url = %q, want %q", got, want)
	}
	if !strings.Contains(topic.DisplayName, "Some Show") {
		t.Errorf("display name: %q", topic.DisplayName)
	}
}

func TestCheckFallsBackToGUIDWhenNoInfoHash(t *testing.T) {
	feedNoHash := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <item>
    <title>Some Release</title>
    <guid>release-12345</guid>
    <enclosure url="magnet:?xt=urn:btih:abc" type="application/x-bittorrent"/>
  </item>
</channel>
</rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(feedNoHash))
	}))
	defer srv.Close()

	testHost := strings.TrimPrefix(srv.URL, "http://")
	p := New(&http.Client{
		Timeout: 5 * time.Second,
		Transport: &e2etest.HostRewriteTransport{
			From: "indexer.example.com",
			To:   testHost,
		},
	})
	topic := &domain.Topic{
		URL: "torznab+https://indexer.example.com/api?apikey=K",
		Extra: map[string]any{
			"indexer_url": "https://indexer.example.com/api?apikey=K",
		},
	}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "release-12345" {
		t.Errorf("hash should fall back to GUID, got %q", check.Hash)
	}
}

func TestCheckErrorsOnEmptyFeed(t *testing.T) {
	emptyFeed := `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(emptyFeed))
	}))
	defer srv.Close()

	testHost := strings.TrimPrefix(srv.URL, "http://")
	p := New(&http.Client{
		Timeout: 5 * time.Second,
		Transport: &e2etest.HostRewriteTransport{
			From: "indexer.example.com",
			To:   testHost,
		},
	})
	topic := &domain.Topic{
		URL: "torznab+https://indexer.example.com/api?apikey=K",
		Extra: map[string]any{
			"indexer_url": "https://indexer.example.com/api?apikey=K",
		},
	}
	if _, err := p.Check(context.Background(), topic, nil); err == nil {
		t.Error("expected error on empty feed")
	}
}

func TestCheckErrorsOnHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	testHost := strings.TrimPrefix(srv.URL, "http://")
	p := New(&http.Client{
		Timeout: 5 * time.Second,
		Transport: &e2etest.HostRewriteTransport{
			From: "indexer.example.com",
			To:   testHost,
		},
	})
	topic := &domain.Topic{
		URL: "torznab+https://indexer.example.com/api?apikey=K",
		Extra: map[string]any{
			"indexer_url": "https://indexer.example.com/api?apikey=K",
		},
	}
	if _, err := p.Check(context.Background(), topic, nil); err == nil {
		t.Error("expected error on HTTP 500")
	}
}

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"The Show S01E12 1080p":  "The.Show.S01E12.1080p",
		"":                       "torznab",
		"Special!@#$%Chars":      "SpecialChars",
		"with-dashes_and_dots.x": "with-dashes_and_dots.x",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
