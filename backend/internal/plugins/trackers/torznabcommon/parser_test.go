package torznabcommon

import (
	"strings"
	"testing"
)

const sampleTorznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
  <title>Example Indexer</title>
  <item>
    <title>The Show S01E12 1080p WEB-DL DDP5.1 H.264-MARAUDER</title>
    <guid>https://example.com/details/9001</guid>
    <pubDate>Mon, 06 Apr 2026 18:00:00 +0000</pubDate>
    <link>http://example.com/download?id=9001</link>
    <enclosure url="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;dn=The.Show.S01E12" length="3145728000" type="application/x-bittorrent"/>
    <torznab:attr name="seeders" value="42"/>
    <torznab:attr name="peers" value="50"/>
    <torznab:attr name="infohash" value="0123456789ABCDEF0123456789ABCDEF01234567"/>
  </item>
  <item>
    <title>The Show S01E11 1080p WEB-DL</title>
    <guid>https://example.com/details/9000</guid>
    <pubDate>Sun, 30 Mar 2026 18:00:00 +0000</pubDate>
    <link>http://example.com/download?id=9000</link>
    <enclosure url="magnet:?xt=urn:btih:1111111111111111111111111111111111111111" length="3145728000" type="application/x-bittorrent"/>
    <torznab:attr name="seeders" value="20"/>
  </item>
</channel>
</rss>`

const sampleNewznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
  <title>NZB Indexer</title>
  <item>
    <title>The Show S01E12 1080p WEB-DL</title>
    <guid>https://nzbexample.com/details/abc</guid>
    <pubDate>Mon, 06 Apr 2026 18:00:00 +0000</pubDate>
    <link>https://nzbexample.com/getnzb/abc</link>
    <enclosure url="https://nzbexample.com/getnzb/abc.nzb" length="3145728000" type="application/x-nzb"/>
    <newznab:attr name="category" value="5040"/>
    <newznab:attr name="size" value="3145728000"/>
  </item>
</channel>
</rss>`

func TestParseTorznabFeed(t *testing.T) {
	items, err := Parse(strings.NewReader(sampleTorznabFeed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	first := items[0]
	if first.Title == "" || !strings.Contains(first.Title, "S01E12") {
		t.Errorf("title: %q", first.Title)
	}
	if first.GUID != "https://example.com/details/9001" {
		t.Errorf("guid: %q", first.GUID)
	}
	if !strings.HasPrefix(first.Enclosure, "magnet:?xt=urn:btih:") {
		t.Errorf("enclosure: %q", first.Enclosure)
	}
	if first.EnclosureType != "application/x-bittorrent" {
		t.Errorf("enclosure type: %q", first.EnclosureType)
	}
	if first.EnclosureLength != 3145728000 {
		t.Errorf("enclosure length: %d", first.EnclosureLength)
	}
	if first.InfoHash() != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("infohash: %q", first.InfoHash())
	}
	if first.Seeders() != 42 {
		t.Errorf("seeders: %d", first.Seeders())
	}
}

func TestParseNewznabFeed(t *testing.T) {
	items, err := Parse(strings.NewReader(sampleNewznabFeed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if it.GUID != "https://nzbexample.com/details/abc" {
		t.Errorf("guid: %q", it.GUID)
	}
	if !strings.HasSuffix(it.Enclosure, ".nzb") {
		t.Errorf("enclosure: %q", it.Enclosure)
	}
	if it.EnclosureType != "application/x-nzb" {
		t.Errorf("enclosure type: %q", it.EnclosureType)
	}
	if it.Attrs["category"] != "5040" {
		t.Errorf("category attr: %q", it.Attrs["category"])
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse(strings.NewReader("")); err == nil {
		t.Error("expected error on empty feed")
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse(strings.NewReader("<rss>not valid xml")); err == nil {
		t.Error("expected error on malformed XML")
	}
}
