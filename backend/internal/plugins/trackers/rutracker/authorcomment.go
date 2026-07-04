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
//     multi-page threads are walked backward (newest page first, capped at
//     maxCommentPageFetches extra fetches) with the already-fetched first
//     page as the final fallback. Best-effort tradeoff: on a thread so
//     long that the author's newest comment sits on an unwalked middle
//     page, "" is returned rather than crawling the whole thread.
var (
	postBlockRe    = regexp.MustCompile(`<tbody id="post_\d+"`)
	nickAuthorRe   = regexp.MustCompile(`class="nick nick-author"`)
	postBodyOpenRe = regexp.MustCompile(`<div class="post_body"[^>]*>`)
)

// maxCommentPageFetches caps how many paginated pages (beyond the first,
// already-fetched one) the backward walk may request per update. Bounds the
// HTTP cost on huge threads; on the rare miss the excerpt is simply absent.
const maxCommentPageFetches = 3

var _ registry.WithAuthorComment = (*plugin)(nil)

// AuthorComment returns the topic author's newest comment as plain text,
// or "" when none is found. Fetches the canonical topic page, then walks
// the pagination backward (see the file comment for the walk bounds).
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

	// Walk the pagination backward (newest page first), capped at
	// maxCommentPageFetches extra round-trips; the already-fetched first
	// page is the final fallback (post #1 there is the release
	// description, never a comment). A page fetch failure just moves the
	// walk along — "nothing found" stays uniformly ("", nil).
	starts := forumcommon.PaginationStarts([]byte(page), id)
	if len(starts) > maxCommentPageFetches {
		starts = starts[:maxCommentPageFetches]
	}
	for _, start := range starts {
		raw, ferr := p.fetchBytes(ctx, nil, creds, fmt.Sprintf("%s&start=%d", canonical, start))
		if ferr != nil {
			continue
		}
		if c := latestAuthorComment(forumcommon.DecodeWindows1251(string(raw)), false); c != "" {
			return c, nil
		}
	}
	return latestAuthorComment(page, true), nil
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
