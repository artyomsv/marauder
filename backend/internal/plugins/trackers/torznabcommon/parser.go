// Package torznabcommon parses the RSS feed format that Torznab and
// Newznab share. Both protocols use the same RSS 2.0 envelope with a
// `torznab:`/`newznab:` namespaced attribute element for extra metadata.
//
// This package is intentionally narrow: parsing only. The torznab and
// newznab plugins build on top of it.
//
// Reference for the schema:
//
//	https://torznab.github.io/spec-1.3-draft/torznab/Specification-v1.3.html
//	https://github.com/Sjmarf/spotweb/wiki/NewzNAB-API
package torznabcommon

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Item is one release in a Torznab/Newznab feed.
type Item struct {
	// Title is the human-readable release name.
	Title string

	// GUID is the indexer's stable identifier for this release. Used as
	// the hash in Marauder's update-detection.
	GUID string

	// Link is the indexer's "details" page URL (often the same as GUID).
	Link string

	// Enclosure is the URL of the actual download — a magnet URI for
	// torrent indexers, an .nzb file URL for Usenet indexers.
	Enclosure string

	// EnclosureType is the MIME type, e.g. "application/x-bittorrent"
	// or "application/x-nzb".
	EnclosureType string

	// EnclosureLength is the byte length of the enclosure if the
	// indexer reported it. Zero means unknown.
	EnclosureLength int64

	// PubDate is the release publish date as the indexer reported it.
	PubDate string

	// Attrs holds the namespaced `torznab:attr` / `newznab:attr` pairs
	// (seeders, peers, infohash, etc.) keyed by name.
	Attrs map[string]string
}

// InfoHash returns the BTIH from the Torznab "infohash" attr if
// present, lower-cased and trimmed.
func (it *Item) InfoHash() string {
	if v, ok := it.Attrs["infohash"]; ok {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return ""
}

// Seeders returns the seeder count, or 0 if missing/unparseable.
func (it *Item) Seeders() int {
	if v, ok := it.Attrs["seeders"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}

// rssChannel is the unmarshalling target for the RSS root.
type rssChannel struct {
	XMLName xml.Name    `xml:"rss"`
	Channel rssChannel2 `xml:"channel"`
}

type rssChannel2 struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title        string       `xml:"title"`
	GUID         string       `xml:"guid"`
	Link         string       `xml:"link"`
	PubDate      string       `xml:"pubDate"`
	Enclosure    rssEnclosure `xml:"enclosure"`
	Attrs        []rssAttr    `xml:"attr"`
	TorznabAttrs []rssAttr    `xml:"http://torznab.com/schemas/2015/feed attr"`
	NewznabAttrs []rssAttr    `xml:"http://www.newznab.com/DTD/2010/feeds/attributes/ attr"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type rssAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// Parse decodes a Torznab/Newznab feed from r and returns the items in
// publication order (newest first, as the indexer publishes them).
//
// Parse is intentionally tolerant: a missing element doesn't fail the
// whole parse, the field just stays empty. The caller asserts on what
// it actually needs.
func Parse(r io.Reader) ([]Item, error) {
	body, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("torznab feed is empty")
	}

	var doc rssChannel
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse XML: %w", err)
	}

	out := make([]Item, 0, len(doc.Channel.Items))
	for _, raw := range doc.Channel.Items {
		it := Item{
			Title:         strings.TrimSpace(raw.Title),
			GUID:          strings.TrimSpace(raw.GUID),
			Link:          strings.TrimSpace(raw.Link),
			Enclosure:     raw.Enclosure.URL,
			EnclosureType: raw.Enclosure.Type,
			PubDate:       strings.TrimSpace(raw.PubDate),
			Attrs:         map[string]string{},
		}
		if raw.Enclosure.Length != "" {
			it.EnclosureLength, _ = strconv.ParseInt(raw.Enclosure.Length, 10, 64)
		}
		for _, a := range raw.TorznabAttrs {
			it.Attrs[strings.ToLower(a.Name)] = a.Value
		}
		for _, a := range raw.NewznabAttrs {
			it.Attrs[strings.ToLower(a.Name)] = a.Value
		}
		// Some indexers (and recorded fixtures) emit attrs without a
		// namespace prefix; capture those too.
		for _, a := range raw.Attrs {
			if _, ok := it.Attrs[strings.ToLower(a.Name)]; !ok {
				it.Attrs[strings.ToLower(a.Name)] = a.Value
			}
		}
		out = append(out, it)
	}
	return out, nil
}
