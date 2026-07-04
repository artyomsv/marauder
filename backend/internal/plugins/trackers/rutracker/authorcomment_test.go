package rutracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// rtPost builds one post block in the shape RuTracker's live viewtopic.php
// emits (verified against the real page 2026-07-04): a <tbody id="post_N">
// row with the poster's nick in the left column — class "nick nick-author"
// marks every post by the topic starter — and the content in a
// <div class="post_body">…</div><!--/post_body--> cell.
func rtPost(id, nickClass, nick, bodyHTML string) string {
	return `<tbody id="post_` + id + `" class="row1">
<tr>
	<td class="poster_info td1 hide-for-print"><a id="` + id + `"></a>
		<p class="` + nickClass + `">` + nick + `</p>
	</td>
	<td class="message td2" rowspan="2">
		<div class="post_wrap" id="p-w-` + id + `">
			<div class="post_body" id="p-` + id + `">` + bodyHTML + `</div><!--/post_body-->
		</div><!--/post_wrap-->
	</td>
</tr>
</tbody>`
}

func topicPage(posts ...string) string {
	return `<html><head><title>Some Show :: RuTracker.org</title></head><body><table>` +
		strings.Join(posts, "\n") + `</table></body></html>`
}

// newAuthorCommentServer serves per-query topic pages: pages[""] is the bare
// viewtopic page, pages["30"] the &start=30 page, etc.
func newAuthorCommentServer(t *testing.T, pages map[string]string) (*plugin, *[]string) {
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
	hostNoScheme := strings.TrimPrefix(srv.URL, "http://")
	p := &plugin{
		sessions:  forumcommon.New(),
		domain:    hostNoScheme,
		transport: &schemeRewrite{target: hostNoScheme},
	}
	return p, &seen
}

func TestAuthorComment_SinglePage_ReturnsLatestAuthorPost(t *testing.T) {
	page := topicPage(
		rtPost("100", "nick nick-author", "uploader", `Release description with <var class="postImg" title="x.jpg"></var>`),
		rtPost("101", "nick ", "commenter", `Thanks!`),
		rtPost("102", "nick nick-author", "uploader",
			`<div class="q-wrap"><div class="q">will there be more?</div></div>Added episode <b>8</b>.`),
	)
	p, _ := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=6511657", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Added episode 8." {
		t.Errorf("AuthorComment = %q, want the author's latest comment with the quote stripped", got)
	}
}

func TestAuthorComment_AuthorOnlyPostedRelease_ReturnsEmpty(t *testing.T) {
	page := topicPage(
		rtPost("100", "nick nick-author", "uploader", `Release description`),
		rtPost("101", "nick ", "commenter", `Thanks!`),
	)
	p, _ := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=1", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "" {
		t.Errorf("AuthorComment = %q, want empty when the author never commented past the release post", got)
	}
}

func TestAuthorComment_MultiPage_ScansLastPage(t *testing.T) {
	page1 := topicPage(rtPost("100", "nick nick-author", "uploader", `Release description`)) +
		`<a class="pg" href="viewtopic.php?t=42&amp;start=30">2</a>`
	lastPage := topicPage(
		rtPost("200", "nick nick-author", "uploader", `Final cut uploaded.`),
		rtPost("201", "nick ", "commenter", `great, thanks`),
	)
	p, seen := newAuthorCommentServer(t, map[string]string{"": page1, "30": lastPage})

	got, err := p.AuthorComment(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=42", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Final cut uploaded." {
		t.Errorf("AuthorComment = %q, want the author's post from the last page", got)
	}
	joined := strings.Join(*seen, " | ")
	if !strings.Contains(joined, "start=30") {
		t.Errorf("expected a fetch of the last page (start=30), saw requests: %s", joined)
	}
}

func TestAuthorComment_Cp1251Body_DecodedToUTF8(t *testing.T) {
	comment, err := charmap.Windows1251.NewEncoder().String("Добавлена 8 серия.")
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	page := topicPage(
		rtPost("100", "nick nick-author", "uploader", `Release description`),
		rtPost("101", "nick nick-author", "uploader", comment),
	)
	p, _ := newAuthorCommentServer(t, map[string]string{"": page})

	got, err := p.AuthorComment(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=1", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "Добавлена 8 серия." {
		t.Errorf("AuthorComment = %q, want decoded UTF-8 Cyrillic", got)
	}
}

func TestAuthorComment_PageWithoutPosts_ReturnsEmpty(t *testing.T) {
	p, _ := newAuthorCommentServer(t, map[string]string{"": `<html><body>maintenance page, no posts</body></html>`})

	got, err := p.AuthorComment(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=1", nil)
	if err != nil {
		t.Fatalf("AuthorComment: %v", err)
	}
	if got != "" {
		t.Errorf("AuthorComment = %q, want empty (fail-open) on a page with no post blocks", got)
	}
}

func TestAuthorComment_NonViewtopicURL_Error(t *testing.T) {
	p := &plugin{sessions: forumcommon.New()}
	if _, err := p.AuthorComment(context.Background(), "https://example.com/x", nil); err == nil {
		t.Fatal("expected an error for a non-rutracker URL")
	}
}
