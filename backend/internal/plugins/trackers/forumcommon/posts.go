package forumcommon

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Shared HTML-scanning helpers for the phpBB-style forum trackers' author
// comment parsing (issue #110). The per-tracker post splitting and author
// attribution stay in each plugin (their markups differ); only the genuinely
// tracker-agnostic pieces live here.

// MaxPaginationStart returns the largest `&start=N` offset among viewtopic
// pagination links for the given topic id, or 0 when the topic fits on one
// page. Both the HTML-escaped (&amp;start=) and raw (&start=) forms match.
func MaxPaginationStart(page []byte, topicID string) int {
	// [?&;] anchors t= to a query-param boundary (`;` is the tail of a
	// HTML-escaped &amp;) so an unrelated param merely ending in "t" (e.g.
	// list=<id>&start=) can't false-positive.
	re := regexp.MustCompile(`[?&;]t=` + regexp.QuoteMeta(topicID) + `&(?:amp;)?start=(\d+)`)
	maxStart := 0
	for _, m := range re.FindAllSubmatch(page, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxStart {
			maxStart = n
		}
	}
	return maxStart
}

// StripTagBlocks removes every `<tag ... class="...classSubstr..." ...>`
// element block from s, including nested same-tag children (depth-balanced).
// Used to drop quote/spoiler/signature blocks before excerpting a post body.
func StripTagBlocks(s, tag, classSubstr string) string {
	openRe := regexp.MustCompile(`<` + regexp.QuoteMeta(tag) + `\b[^>]*class="[^"]*` + regexp.QuoteMeta(classSubstr) + `[^"]*"[^>]*>`)
	for {
		loc := openRe.FindStringIndex(s)
		if loc == nil {
			return s
		}
		end, ok := matchingClose(s, loc[1], tag)
		if !ok {
			// Unbalanced markup: drop from the opening tag to the end rather
			// than looping forever.
			return s[:loc[0]]
		}
		s = s[:loc[0]] + s[end:]
	}
}

// TagBlockInner returns the inner HTML of the first element whose opening
// tag matches openRe, balancing nested same-tag children. openRe must match
// the opening tag itself (e.g. `<div class="postbody"[^>]*>`). ok is false
// when no opening tag is found or the markup never closes it.
func TagBlockInner(s string, openRe *regexp.Regexp, tag string) (string, bool) {
	loc := openRe.FindStringIndex(s)
	if loc == nil {
		return "", false
	}
	end, ok := matchingClose(s, loc[1], tag)
	if !ok {
		return "", false
	}
	return s[loc[1] : end-len("</"+tag+">")], true
}

// maxScanIterations bounds the matchingClose token walk. Each iteration
// rescans the remaining string, so pathological input (thousands of nested
// same-tag openings in an attacker-controlled page) would otherwise degrade
// to O(n²) CPU that a context timeout cannot interrupt. Real forum posts
// nest a handful of levels; hitting this cap means garbage input, and the
// callers all fail open on ok=false.
const maxScanIterations = 10_000

// matchingClose scans s from `from` (just past an opening <tag ...>) and
// returns the index right after the matching </tag>, balancing nested
// openings of the same tag.
func matchingClose(s string, from int, tag string) (int, bool) {
	openTok := "<" + tag
	closeTok := "</" + tag + ">"
	depth := 1
	i := from
	for iter := 0; depth > 0; iter++ {
		if iter >= maxScanIterations {
			return 0, false
		}
		nextOpen := strings.Index(s[i:], openTok)
		nextClose := strings.Index(s[i:], closeTok)
		if nextClose == -1 {
			return 0, false
		}
		// An opening token only counts when it is a real tag start
		// (followed by a space, '>' or attribute), which `<div` prefix
		// matching approximates well enough for forum markup.
		if nextOpen != -1 && nextOpen < nextClose {
			i += nextOpen + len(openTok)
			depth++
			continue
		}
		i += nextClose + len(closeTok)
		depth--
	}
	return i, true
}

var (
	// separatorTagRe matches block/line-break tags that must become word
	// separators (`a<br>b` is two words); everything else is inline
	// formatting whose removal must NOT split words (`<b>8</b>.` is "8.").
	separatorTagRe = regexp.MustCompile(`(?i)</?(?:br|p|div|td|tr|table|li|ul|ol|hr)\b[^>]*>`)
	// tagRe matches any remaining HTML tag for plain removal.
	tagRe = regexp.MustCompile(`<[^>]*>`)
)

// HTMLToText flattens an HTML fragment to plain text: block-level tags
// become word separators, inline tags are dropped, entities are decoded,
// and all whitespace is collapsed to single spaces. The input is expected
// to already be valid UTF-8 (decode cp1251 pages with DecodeWindows1251
// first).
func HTMLToText(fragment string) string {
	s := separatorTagRe.ReplaceAllString(fragment, " ")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
