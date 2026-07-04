package nnmclub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// nnmPost builds one post row in the shape NNM-Club's live viewtopic.php
// emits (verified against the real page 2026-07-04): the poster column holds
// an <a name="POSTID"> anchor and the username inside a class="genmed" span
// (possibly wrapped in a styled <SPAN>), and the content sits in a
// <div class="postbody"> inside <table id="post_POSTID">.
func nnmPost(id, authorHTML, bodyHTML string) string {
	return `<tr class="row1">
	<td width="150" align="left" valign="top" class="row1"><a name="` + id + `"></a>
		<span class="genmed" style="padding-top: 2px" title="">` + authorHTML + `<br></span>
		<div>Стаж: 18 лет</div>
	</td>
	<td class="row1" width="100%" valign="top">
		<table id="post_` + id + `" width="100%"><tr>
			<td colspan="2"><div class="postbody" style="overflow:auto">` + bodyHTML + `</div></td>
		</tr></table>
	</td>
</tr>`
}

func nnmTopicPage(posts ...string) string {
	return `<html><head><title>Some Movie :: NNM-Club</title></head><body><table class="forumline">` +
		strings.Join(posts, "\n") + `</table></body></html>`
}

func newAuthorCommentServer(t *testing.T, pages map[string]string) (*plugin, string, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php") {
			w.WriteHeader(404)
			return
		}
		seen = append(seen, r.URL.RawQuery)
		page, ok := pages[r.URL.Query().Get("start")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	// The plugin's SSRF allowlist only admits nnmclub hosts, so tests hit it
	// with the real hostname and rewrite the dial target instead (shared
	// hostRewrite transport from nnmclub_test.go).
	p := &plugin{
		sessions:  forumcommon.New(),
		transport: &hostRewrite{host: strings.TrimPrefix(srv.URL, "http://")},
	}
	return p, "https://nnmclub.to/forum/viewtopic.php?t=830137", &seen
}

func TestAuthorComment_SinglePage_ReturnsLatestAuthorPost(t *testing.T) {
	page := nnmTopicPage(
		nnmPost("100", `<SPAN style="color : #ff0004; font-weight : bold;">uploader</SPAN>`, `Release description <hr /> details`),
		nnmPost("101", `commenter`, `Спасибо!`),
		nnmPost("102", `<SPAN style="color : #ff0004; font-weight : bold;">uploader</SPAN>`, `Обновлено: добавлена 8 серия.`),
		nnmPost("103", `commenter`, `great`),
	)
	p, topicURL, _ := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Обновлено: добавлена 8 серия." {
		t.Errorf("AuthorComment = %q, want the uploader's latest comment", got)
	}
}

func TestAuthorComment_AuthorOnlyPostedRelease_ReturnsEmpty(t *testing.T) {
	page := nnmTopicPage(
		nnmPost("100", `uploader`, `Release description`),
		nnmPost("101", `commenter`, `thanks`),
	)
	p, topicURL, _ := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "" {
		t.Errorf("AuthorComment = %q, want empty when the author never commented", got)
	}
}

func TestAuthorComment_MultiPage_MatchesAuthorByNameOnLastPage(t *testing.T) {
	page1 := nnmTopicPage(nnmPost("100", `uploader`, `Release description`)) +
		`<a href="viewtopic.php?t=830137&amp;start=15">2</a>`
	lastPage := nnmTopicPage(
		nnmPost("200", `uploader`, `Раздача обновлена.<td class="quote">old quoted text</td>`),
		nnmPost("201", `commenter`, `ok`),
	)
	p, topicURL, seen := newAuthorCommentServer(t, map[string]string{"": page1, "15": lastPage})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Раздача обновлена." {
		t.Errorf("AuthorComment = %q, want the author's last-page comment with the quote stripped", got)
	}
	joined := strings.Join(*seen, " | ")
	if !strings.Contains(joined, "start=15") {
		t.Errorf("expected a fetch of the last page (start=15), saw requests: %s", joined)
	}
}

// TestAuthorComment_AuthorOnlyOnPageOne_FallsBackToCachedFirstPage mirrors
// the rutracker two-page blind-spot case: author comments only on page 1,
// last page all other users — must fall back to the cached page 1 without
// a third fetch.
func TestAuthorComment_AuthorOnlyOnPageOne_FallsBackToCachedFirstPage(t *testing.T) {
	page1 := nnmTopicPage(
		nnmPost("100", `uploader`, `Release description`),
		nnmPost("101", `uploader`, `Обновлено: серия 6.`),
	) + `<a href="viewtopic.php?t=830137&amp;start=15">2</a>`
	lastPage := nnmTopicPage(
		nnmPost("200", `commenter`, `nice`),
	)
	p, topicURL, seen := newAuthorCommentServer(t, map[string]string{"": page1, "15": lastPage})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Обновлено: серия 6." {
		t.Errorf("AuthorComment = %q, want the page-1 author comment via fallback", got)
	}
	if len(*seen) != 2 {
		t.Errorf("fetches = %d (%v), want exactly 2 — page 1 must be reused", len(*seen), *seen)
	}
}

// TestAuthorComment_WalkCappedAtMaxPages mirrors the rutracker cap test:
// a 5-page thread whose only author comment sits beyond the 3-page walk cap
// yields "" with exactly 1 + maxCommentPageFetches fetches.
func TestAuthorComment_WalkCappedAtMaxPages(t *testing.T) {
	pg := `<a href="viewtopic.php?t=830137&amp;start=15">2</a>` +
		`<a href="viewtopic.php?t=830137&amp;start=30">3</a>` +
		`<a href="viewtopic.php?t=830137&amp;start=45">4</a>` +
		`<a href="viewtopic.php?t=830137&amp;start=60">5</a>`
	replies := nnmTopicPage(nnmPost("300", `commenter`, `ok`)) + pg
	pages := map[string]string{
		"":   nnmTopicPage(nnmPost("100", `uploader`, `Release description`)) + pg,
		"15": nnmTopicPage(nnmPost("200", `uploader`, `Beyond the cap.`)) + pg,
		"30": replies,
		"45": replies,
		"60": replies,
	}
	p, topicURL, seen := newAuthorCommentServer(t, pages)

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "" {
		t.Errorf("AuthorComment = %q, want empty — the only author comment is beyond the walk cap", got)
	}
	if len(*seen) != 4 {
		t.Errorf("fetches = %d (%v), want 4: page 1 + exactly maxCommentPageFetches walked pages", len(*seen), *seen)
	}
	if joined := strings.Join(*seen, " | "); strings.Contains(joined, "start=15") {
		t.Errorf("page start=15 is beyond the 3-page cap and must not be fetched: %s", joined)
	}
}

// TestAuthorComment_LastPageFetchFails_FallsBackToPageOne asserts a failed
// last-page fetch degrades to scanning the already-fetched first page
// instead of surfacing a transient error.
func TestAuthorComment_LastPageFetchFails_FallsBackToPageOne(t *testing.T) {
	page1 := nnmTopicPage(
		nnmPost("100", `uploader`, `Release description`),
		nnmPost("101", `uploader`, `Добавлена 5 серия.`),
	) + `<a href="viewtopic.php?t=830137&amp;start=15">2</a>`
	// start=15 is NOT served → the last-page fetch 404s.
	p, topicURL, _ := newAuthorCommentServer(t, map[string]string{"": page1})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment must fall back, not error: %v", err)
	}
	if got != "Добавлена 5 серия." {
		t.Errorf("AuthorComment = %q, want the page-1 author comment as fallback", got)
	}
}

// TestAuthorComment_DeepLinkURL_CanonicalizedToPageOne guards author
// attribution against stored topic URLs that already carry pagination or
// fragments (e.g. a user pasted a page-4 deep link). The first fetch must be
// the canonical page-1 URL — otherwise posts[0] is a random replier, not the
// release author.
func TestAuthorComment_DeepLinkURL_CanonicalizedToPageOne(t *testing.T) {
	page := nnmTopicPage(
		nnmPost("100", `uploader`, `Release description`),
		nnmPost("101", `uploader`, `Добавлена 8 серия.`),
	)
	// Only the canonical (no start=) query is served: a raw deep-link fetch
	// with start=90 would 404 and fail the test.
	p, _, seen := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(),
		"https://nnmclub.to/forum/viewtopic.php?t=830137&start=90", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Добавлена 8 серия." {
		t.Errorf("AuthorComment = %q, want the author's comment from the canonical page", got)
	}
	for _, q := range *seen {
		if strings.Contains(q, "start=90") {
			t.Errorf("fetched the raw deep-link URL (query %q); must canonicalize to page 1", q)
		}
	}
}

func TestAuthorComment_PageWithoutPosts_ReturnsEmpty(t *testing.T) {
	p, topicURL, _ := newAuthorCommentServer(t, map[string]string{"": `<html><body>maintenance page, no posts</body></html>`})

	got, err := p.AuthorComment(context.Background(), topicURL, nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "" {
		t.Errorf("AuthorComment = %q, want empty (fail-open) on a page with no post anchors", got)
	}
}

func TestAuthorComment_OffSiteURL_Refused(t *testing.T) {
	p := &plugin{sessions: forumcommon.New()}
	if _, err := p.AuthorComment(context.Background(), "https://evil.example/forum/viewtopic.php?t=1", nil); err == nil {
		t.Fatal("expected an error for an off-site URL")
	}
}
