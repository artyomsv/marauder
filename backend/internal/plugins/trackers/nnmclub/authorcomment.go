package nnmclub

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
// live viewtopic.php markup 2026-07-04. NNM-Club's phpBB 2.x skin has no
// author-marking class (unlike RuTracker's nick-author), so the topic
// author is the first post's poster name and later posts are matched by
// name:
//
//   - each post starts with an <a name="POSTID"> anchor in the poster
//     column; the username sits in the first class="genmed" span after it
//     (sometimes wrapped in a styled <SPAN>, terminated by <br>);
//   - the content lives in <div class="postbody"> inside
//     <table id="post_POSTID">;
//   - quoted text sits in <td class="quote"> cells, stripped before
//     excerpting;
//   - pagination links carry &start=N (15 posts per page).
var (
	postAnchorRe = regexp.MustCompile(`<a name="\d+"></a>`)
	// genmedNameRe captures the username markup up to the terminating <br>
	// or the span close. (?is) because the wrapping styled SPAN's case
	// varies in the live markup.
	genmedNameRe   = regexp.MustCompile(`(?is)<span class="genmed"[^>]*>(.*?)(?:<br\s*/?>|</span>)`)
	postbodyOpenRe = regexp.MustCompile(`<div class="postbody"[^>]*>`)
)

// maxCommentPageFetches caps how many paginated pages (beyond the first,
// already-fetched one) the backward walk may request per update.
const maxCommentPageFetches = 3

var _ registry.WithAuthorComment = (*plugin)(nil)

// AuthorComment returns the topic author's newest comment as plain text, or
// "" when none is found. Fetches the canonical topic page (which names the
// author in post #1), then walks the pagination backward — newest page
// first, capped at maxCommentPageFetches extra fetches — falling back to
// the cached first page, so a comment on any of the last few pages or page
// 1 is found. Anonymous like the rest of the plugin; p.fetch enforces the
// NNM-Club host allowlist.
func (p *plugin) AuthorComment(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (string, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return "", errors.New("author comment: not a nnm-club viewtopic URL")
	}
	// m[0] is the matched "https://host/forum/viewtopic.php?t=N" prefix —
	// the canonical page-1 URL. Fetching it (never the raw URL) keeps
	// author attribution correct when the stored URL is a deep link that
	// already carries &start= or a fragment: posts[0] must be the release
	// post, and the &start= appended below must be the only pagination.
	canonical := m[0]
	raw, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return "", fmt.Errorf("author comment: %w", err)
	}
	page := forumcommon.DecodeWindows1251(string(raw))
	posts := parsePosts(page)
	if len(posts) == 0 {
		return "", nil
	}
	author := posts[0].author
	if author == "" {
		return "", nil
	}

	// Walk the pagination backward (newest page first), capped at
	// maxCommentPageFetches extra round-trips; the already-fetched first
	// page is the final fallback (post #1 there is the release
	// description, never a comment). A page fetch failure just moves the
	// walk along — "nothing found" stays uniformly ("", nil).
	starts := forumcommon.PaginationStarts([]byte(page), m[1])
	if len(starts) > maxCommentPageFetches {
		starts = starts[:maxCommentPageFetches]
	}
	for _, start := range starts {
		raw, ferr := p.fetch(ctx, fmt.Sprintf("%s&start=%d", canonical, start), creds)
		if ferr != nil {
			continue
		}
		pagePosts := parsePosts(forumcommon.DecodeWindows1251(string(raw)))
		if c := latestBy(pagePosts, author, 0); c != "" {
			return c, nil
		}
	}
	return latestBy(posts, author, 1), nil
}

// latestBy returns the newest non-empty post by author at index >= first,
// scanning bottom-up. first=1 skips the release post on page 1.
func latestBy(posts []post, author string, first int) string {
	for i := len(posts) - 1; i >= first; i-- {
		if posts[i].author != author {
			continue
		}
		if text := posts[i].text(); text != "" {
			return text
		}
	}
	return ""
}

// post is one parsed forum post: the poster's name and the raw body HTML.
type post struct {
	author string
	body   string
}

// text flattens the post body, dropping quoted text and signatures.
func (p post) text() string {
	b := forumcommon.StripTagBlocks(p.body, "td", "quote")
	b = forumcommon.StripTagBlocks(b, "div", "signature")
	b = forumcommon.StripTagBlocks(b, "span", "signature")
	return forumcommon.HTMLToText(b)
}

// parsePosts splits a topic page into posts on the <a name="POSTID">
// anchors, extracting each poster's name and body. Unparsable segments are
// skipped (best-effort).
func parsePosts(page string) []post {
	locs := postAnchorRe.FindAllStringIndex(page, -1)
	posts := make([]post, 0, len(locs))
	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[loc[0]:end]
		var author string
		if m := genmedNameRe.FindStringSubmatch(block); m != nil {
			author = forumcommon.HTMLToText(m[1])
		}
		body, ok := forumcommon.TagBlockInner(block, postbodyOpenRe, "div")
		if !ok {
			continue
		}
		posts = append(posts, post{author: author, body: body})
	}
	return posts
}
