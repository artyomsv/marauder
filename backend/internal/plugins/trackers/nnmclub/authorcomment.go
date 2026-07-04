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

var _ registry.WithAuthorComment = (*plugin)(nil)

// AuthorComment returns the topic author's newest comment on the scanned
// page as plain text, or "" when none is found there. At most two
// round-trips: the canonical topic page (which names the author in post #1),
// plus the last pagination page when the thread spans several — an author
// comment older than the final page of replies is missed by design (the
// two-fetch cap). Anonymous like the rest of the plugin; p.fetch enforces
// the NNM-Club host allowlist.
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

	// Post #1 on the first page is the release description, not a comment.
	skipFirst := true
	if maxStart := forumcommon.MaxPaginationStart([]byte(page), m[1]); maxStart > 0 {
		lastRaw, lerr := p.fetch(ctx, fmt.Sprintf("%s&start=%d", canonical, maxStart), creds)
		if lerr == nil {
			posts = parsePosts(forumcommon.DecodeWindows1251(string(lastRaw)))
			skipFirst = false
		}
		// On a last-page fetch failure fall back to scanning the already-
		// fetched first page: an older author comment beats a transient
		// error, and "nothing found" stays uniformly ("", nil).
	}

	first := 0
	if skipFirst {
		first = 1
	}
	for i := len(posts) - 1; i >= first; i-- {
		if posts[i].author != author {
			continue
		}
		if text := posts[i].text(); text != "" {
			return text, nil
		}
	}
	return "", nil
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
