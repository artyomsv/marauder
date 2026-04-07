package lostfilm

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// Selectors / patterns. These are the most likely things to drift when
// LostFilm changes its HTML — keeping them as named constants makes a
// future fix a one-line edit.
var (
	// data-code="<showid>-<season>-<episode>" — present on every
	// episode block on the series page. Real format uses HYPHENS,
	// not colons (verified against the live site).
	dataCodeRe = regexp.MustCompile(`data-code="(\d+)-(\d+)-(\d+)"`)

	// data-episode="<show><sss><eee>" — packed integer form of the
	// same triple. Used as a fallback when data-code is missing.
	dataEpisodeRe = regexp.MustCompile(`data-episode="(\d{7,})"`)

	// titleRe extracts the page title for the human-readable display name.
	titleRe = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)

	// Magnet fallback — preserved so the e2e test fixture (a series
	// page with a direct magnet link) keeps working without simulating
	// the full redirector chain.
	magnetRe = regexp.MustCompile(`(magnet:\?xt=urn:btih:[A-Fa-f0-9]+[^"'&\s]*)`)
)

// episodeRef is one (show_id, season, episode) triple parsed from the
// series page.
type episodeRef struct {
	ShowID  string
	Season  int
	Episode int
}

// PackedID encodes the triple into LostFilm's packed integer format:
// `<show><sss><eee>` with season + episode zero-padded to 3 digits.
// Example: show 791, season 2, episode 6 → "791002006".
func (e episodeRef) PackedID() string {
	return fmt.Sprintf("%s%03d%03d", e.ShowID, e.Season, e.Episode)
}

// SeasonEpisodeKey is the human-readable form (s02e06).
func (e episodeRef) SeasonEpisodeKey() string {
	return fmt.Sprintf("s%02de%02d", e.Season, e.Episode)
}

// parseEpisodes extracts every (show, season, episode) triple from the
// series page body. It tries `data-code` first (the canonical hyphen
// form), falls back to `data-episode` (the packed integer form), and
// returns a deduplicated list sorted ascending by (season, episode).
func parseEpisodes(body []byte) []episodeRef {
	out := make([]episodeRef, 0, 16)
	seen := map[string]struct{}{}

	// Pass 1 — data-code="<show>-<season>-<episode>"
	for _, m := range dataCodeRe.FindAllSubmatch(body, -1) {
		s, _ := strconv.Atoi(string(m[2])) // regex guarantees digit-only matches
		e, _ := strconv.Atoi(string(m[3])) // regex guarantees digit-only matches
		if s == 0 || e == 0 {
			continue
		}
		key := string(m[1]) + "-" + string(m[2]) + "-" + string(m[3])
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, episodeRef{ShowID: string(m[1]), Season: s, Episode: e})
	}

	// Pass 2 — data-episode="<packed>" — only used if pass 1 found
	// nothing. Decoding: pop the last 3 digits as episode, the next
	// 3 as season, the rest as show id.
	if len(out) == 0 {
		for _, m := range dataEpisodeRe.FindAllSubmatch(body, -1) {
			packed := string(m[1])
			if len(packed) < 7 {
				continue
			}
			ep, _ := strconv.Atoi(packed[len(packed)-3:])           // regex guarantees digits
			se, _ := strconv.Atoi(packed[len(packed)-6 : len(packed)-3]) // regex guarantees digits
			showID := packed[:len(packed)-6]
			if ep == 0 || se == 0 || showID == "" {
				continue
			}
			key := showID + "-" + strconv.Itoa(se) + "-" + strconv.Itoa(ep)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, episodeRef{ShowID: showID, Season: se, Episode: ep})
		}
	}

	// Sort ascending by (season, episode). Real LostFilm series often
	// list 100+ episodes, so sort.Slice is the right call here — the
	// previous insertion-sort comment was wrong.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}
