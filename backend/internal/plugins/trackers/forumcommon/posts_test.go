package forumcommon

import (
	"regexp"
	"testing"
)

func TestMaxPaginationStart(t *testing.T) {
	tests := []struct {
		name    string
		page    string
		topicID string
		want    int
	}{
		{"no pagination links", `<html><a href="viewtopic.php?t=1">x</a></html>`, "1", 0},
		{
			"picks the largest start offset",
			`<a class="pg" href="viewtopic.php?t=6511657&amp;start=30">2</a>
			 <a class="pg" href="viewtopic.php?t=6511657&amp;start=420">15</a>
			 <a class="pg" href="viewtopic.php?t=6511657&amp;start=60">3</a>`,
			"6511657", 420,
		},
		{
			"unescaped ampersand form also matches",
			`<a href="viewtopic.php?t=99&start=15">2</a>`,
			"99", 15,
		},
		{
			"other topics' pagination is ignored",
			`<a href="viewtopic.php?t=123&amp;start=90">2</a>`,
			"6511657", 0,
		},
		{
			"a param merely ending in t is not a false positive",
			`<a href="index.php?list=6511657&amp;start=5">x</a>`,
			"6511657", 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaxPaginationStart([]byte(tt.page), tt.topicID); got != tt.want {
				t.Errorf("MaxPaginationStart() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStripTagBlocks(t *testing.T) {
	tests := []struct {
		name  string
		html  string
		tag   string
		class string
		want  string
	}{
		{
			"removes a simple block",
			`before<div class="q-wrap"><div class="q">quoted</div></div>after`,
			"div", "q-wrap",
			"beforeafter",
		},
		{
			"handles nested same-tag children",
			`a<div class="sp-wrap">x<div>inner</div>y</div>b`,
			"div", "sp-wrap",
			"ab",
		},
		{
			"removes every matching block",
			`<td class="quote">q1</td>keep<td class="quote">q2</td>`,
			"td", "quote",
			"keep",
		},
		{
			"leaves non-matching blocks alone",
			`<div class="post_body">text</div>`,
			"div", "q-wrap",
			`<div class="post_body">text</div>`,
		},
		{
			"unbalanced markup drops from the opening tag to the end",
			`keep<div class="q-wrap">never closed`,
			"div", "q-wrap",
			"keep",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripTagBlocks(tt.html, tt.tag, tt.class); got != tt.want {
				t.Errorf("StripTagBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTagBlockInner(t *testing.T) {
	openRe := regexp.MustCompile(`<div class="postbody"[^>]*>`)
	tests := []struct {
		name   string
		html   string
		want   string
		wantOK bool
	}{
		{
			"simple block",
			`<div class="postbody" style="x">hello</div>`,
			"hello", true,
		},
		{
			"nested divs stay balanced",
			`<div class="postbody"><div>inner</div>tail</div><div>next</div>`,
			"<div>inner</div>tail", true,
		},
		{
			"three-level-deep nesting stays balanced",
			`<div class="postbody"><div>a<div>b</div>c</div>d</div><div>next</div>`,
			"<div>a<div>b</div>c</div>d", true,
		},
		{
			"a tag merely prefixed with the name does not inflate depth",
			`<div class="postbody">x<divider>y</divider>z</div>tail`,
			"x<divider>y</divider>z", true,
		},
		{"not found", `<span>nothing here</span>`, "", false},
		{"unbalanced returns not-ok", `<div class="postbody">never closed`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TagBlockInner(tt.html, openRe, "div")
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("TagBlockInner() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"tags stripped and whitespace collapsed",
			"\t\tYes! <img class=\"smile\" src=\"x.gif\" alt=\"\"> Added <b>episode 8</b>.\n\t\t",
			"Yes! Added episode 8.",
		},
		{
			"entities decoded",
			"5 &gt; 4 &amp; a&nbsp;b",
			"5 > 4 & a b",
		},
		{
			"br becomes a separator, not a word-join",
			"line one<br>line two",
			"line one line two",
		},
		{"empty", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTMLToText(tt.in); got != tt.want {
				t.Errorf("HTMLToText() = %q, want %q", got, tt.want)
			}
		})
	}
}
