package rutracker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// Author-comment extraction (issue #110). Selectors verified against the
// live viewtopic.php markup 2026-07-04:
//
//   - each post is a <tbody id="post_N"> block;
//   - RuTracker stamps class="nick nick-author" on the poster nick of EVERY
//     post made by the topic starter, so author attribution is a class
//     match, no name comparison needed;
//   - the content lives in <div class="post_body">…; quotes are
//     <div class="q-wrap"> blocks and spoilers <div class="sp-wrap">,
//     both stripped before excerpting;
//   - pagination links are <a class="pg" href="viewtopic.php?t=N&start=M">;
//     on multi-page threads only the page with the largest start is
//     scanned. Best-effort tradeoff: if the author's newest comment sits on
//     an earlier page (the final page holds only other users' replies), it
//     is missed and "" is returned — the cost of capping the feature at two
//     round-trips instead of walking the whole thread.
var (
	postBlockRe    = regexp.MustCompile(`<tbody id="post_\d+"`)
	nickAuthorRe   = regexp.MustCompile(`class="nick nick-author"`)
	postBodyOpenRe = regexp.MustCompile(`<div class="post_body"[^>]*>`)
)

var _ registry.WithAuthorComment = (*plugin)(nil)

// AuthorComment returns the topic author's newest comment on the scanned
// page as plain text, or "" when none is found there. At most two
// round-trips: the canonical topic page, plus the last pagination page when
// the thread spans several (see the package comment for the earlier-page
// limitation).
func (p *plugin) AuthorComment(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (string, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return "", errors.New("author comment: not a rutracker viewtopic URL")
	}
	id := m[1]
	// Rebuild from the trusted host + numeric id (same SSRF stance as
	// ResolveMetadata): never fetch the raw user-supplied URL.
	canonical := fmt.Sprintf("https://%s/forum/viewtopic.php?t=%s", p.domain, id)
	raw, err := p.fetchBytes(ctx, nil, creds, canonical)
	if err != nil {
		return "", fmt.Errorf("author comment: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(raw))

	// Post #1 on the first page is the release description, not a comment.
	skipFirst := true
	if maxStart := forumcommon.MaxPaginationStart([]byte(page), id); maxStart > 0 {
		lastRaw, lerr := p.fetchBytes(ctx, nil, creds, fmt.Sprintf("%s&start=%d", canonical, maxStart))
		if lerr == nil {
			page = forumcommon.DecodeWindows1251(string(lastRaw))
			skipFirst = false
		}
		// On a last-page fetch failure fall back to scanning the already-
		// fetched first page: an older author comment beats a transient
		// error, and "nothing found" stays uniformly ("", nil).
	}
	return latestAuthorComment(page, skipFirst), nil
}

// latestAuthorComment scans a topic page's posts bottom-up for the newest
// one stamped nick-author and returns its body as flattened text. Best-
// effort: an unparsable block is skipped, no match yields "".
func latestAuthorComment(page string, skipFirst bool) string {
	locs := postBlockRe.FindAllStringIndex(page, -1)
	first := 0
	if skipFirst {
		first = 1
	}
	for i := len(locs) - 1; i >= first; i-- {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[locs[i][0]:end]
		if !nickAuthorRe.MatchString(block) {
			continue
		}
		inner, ok := forumcommon.TagBlockInner(block, postBodyOpenRe, "div")
		if !ok {
			continue
		}
		inner = forumcommon.StripTagBlocks(inner, "div", "q-wrap")
		inner = forumcommon.StripTagBlocks(inner, "div", "sp-wrap")
		if text := forumcommon.HTMLToText(inner); text != "" {
			return text
		}
	}
	return ""
}
