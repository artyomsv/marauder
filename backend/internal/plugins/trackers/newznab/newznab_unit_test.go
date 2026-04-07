package newznab

import (
	"context"
	"strings"
	"testing"
)

func TestCanParse(t *testing.T) {
	p := New(nil)
	cases := map[string]bool{
		"newznab+https://nzbgeek.info/api?apikey=K&t=search":     true,
		"newznab+http://localhost:5076/api":                       true,
		"torznab+https://example.com/api":                         false,
		"https://nzbgeek.info/api?apikey=K&t=search":              false,
		"":                                                         false,
	}
	for url, want := range cases {
		if got := p.CanParse(url); got != want {
			t.Errorf("CanParse(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParseExtractsIndexerURL(t *testing.T) {
	p := New(nil)
	topic, err := p.Parse(context.Background(),
		"newznab+https://nzbgeek.info/api?apikey=KEY&t=tvsearch&q=Some+Show&season=1")
	if err != nil {
		t.Fatal(err)
	}
	if topic.TrackerName != "newznab" {
		t.Errorf("tracker name: %s", topic.TrackerName)
	}
	got, _ := topic.Extra["indexer_url"].(string)
	if !strings.HasPrefix(got, "https://nzbgeek.info/api?") {
		t.Errorf("indexer_url = %q", got)
	}
	if !strings.Contains(topic.DisplayName, "Some Show") {
		t.Errorf("display name: %q", topic.DisplayName)
	}
}

func TestParseRejectsBadScheme(t *testing.T) {
	p := New(nil)
	if _, err := p.Parse(context.Background(), "https://nzbgeek.info/api?apikey=K"); err == nil {
		t.Error("expected error on URL without newznab+ prefix")
	}
}
