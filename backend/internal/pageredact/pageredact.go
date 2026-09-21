// Package pageredact strips secrets out of a tracker page before Marauder
// hands it to a user to attach to a bug report.
//
// It exists because of issue #186, which took five days to diagnose and was
// finally solved by one CSS class in one table cell that our account never
// sees. The only way to get that markup is to ask the reporter for it — and
// the page we fetch from a login-gated tracker carries a live session id in
// its own links (`sid=…` on tapochek.net, measured 2026-09-21). Handing a
// user a "copy this" button over that page without redaction would invite
// them to paste an account takeover into a public issue.
//
// The contract has two halves and both matter:
//
//   - No known secret survives.
//   - Every other byte is untouched. Tags, classes, attributes, quotes,
//     whitespace and character-reference spellings are what the report is FOR;
//     a redactor that reformats the page destroys the evidence it collects.
//
// # Why this is not a set of regular expressions
//
// It was, for three review rounds, and each round's fix opened the next hole:
// a pattern that required `name` before `value` leaked the reversed order; a
// quote-aware scan let an unterminated quote swallow the following tag;
// bounding quoted runs at `<` then broke `title="1 < 2"`. An independent review
// then found eleven more, each with an exact input. HTML's lexical rules —
// quoting, comments, raw text, character references — are not something a
// pattern learns one bug at a time.
//
// So the page is tokenised with golang.org/x/net/html, but ONLY to learn where
// each token starts and ends and what kind it is. The page is never parsed into
// a tree and never re-serialised: rendering lowercases tags, re-quotes
// attributes and re-encodes entities, which is exactly the evidence this
// package must not touch. Every decision is recorded as a replacement of a byte
// span in the ORIGINAL page, and everything outside those spans is copied
// through unchanged.
//
// Classification happens on DECODED text, so `form_to&#107;en` is recognised
// as `form_token`; and every decoded byte remembers the source bytes it came
// from, so a replacement always lands on the original spelling.
//
// # Ambiguity
//
// A malformed tag is redacted conservatively rather than refused. A tag with a
// duplicated attribute, a quote inside an attribute name, or no closing `>` is
// scanned a second time as raw text and for any tags it may have swallowed;
// and a duplicated `name` attribute marks a credential field if ANY of its
// values is one, even where a browser would honour only the first. A public
// source file carries every copy, so the browser's choice is not the one that
// matters here. Redaction is still best-effort on a page nobody here controls;
// the UI says so and asks the user to skim the file.
package pageredact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Placeholder replaces every redacted value. It is deliberately loud and
// contains no characters that need HTML escaping, so a redacted page still
// renders and a reader can see that something was removed rather than
// wondering whether the tracker served an empty attribute.
const Placeholder = "MARAUDER-REDACTED"

// ErrUnsafe is returned when the page could not be covered completely. It
// never fires on an ordinary malformed page — those are redacted
// conservatively — but the only alternative to refusing is returning bytes
// that were never examined, and for a file the user is about to publish that
// is a credential leak.
var ErrUnsafe = errors.New("pageredact: the page could not be safely prepared for export")

// Redact returns page with every known secret replaced by Placeholder.
//
// username is the reporter's own tracker account name, redacted so a bug
// report does not out which account they use. It is optional; an empty string
// redacts no name rather than matching everywhere.
//
// The returned slice is a new allocation; page is not modified.
func Redact(page []byte, username string) ([]byte, error) {
	if len(page) == 0 {
		return page, nil
	}
	r := &redactor{page: page, name: strings.TrimSpace(username)}
	if err := r.scan(); err != nil {
		return nil, err
	}
	return r.edits.apply(page), nil
}

type redactor struct {
	page  []byte
	name  string
	edits editList
	// stream is the page's text since the last block boundary. Inline
	// formatting markup does not end it, so `Cookie: <b>session=…</b>` is
	// still read as one line.
	stream mapped
}

// scan walks the token stream and records edits. Only each token's length
// and kind are taken from the tokenizer; content is always read from the
// original page by offset, so nothing depends on the tokenizer's buffer.
func (r *redactor) scan() error {
	z := html.NewTokenizer(bytes.NewReader(r.page))
	off := 0
	textMode := ""
	for {
		tt := z.Next()
		start := off
		off += len(z.Raw())
		switch tt {
		case html.ErrorToken:
			return r.finish(z.Err(), start, off)
		case html.TextToken:
			r.text(start, off, textMode)
			continue // raw-text content may arrive in several tokens
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			r.boundary(string(name))
			r.tag(start, off, false)
			textMode = ""
			if tt == html.StartTagToken {
				textMode = rawTextMode[string(name)]
			}
			continue
		case html.EndTagToken:
			name, _ := z.TagName()
			r.boundary(string(name))
		case html.CommentToken:
			r.flush()
			r.isolated(start, off, false)
		default: // doctype
			r.flush()
		}
		textMode = ""
	}
}

// finish handles the tokenizer's final token. At EOF its raw bytes are
// whatever the page ended inside — most importantly a tag cut off before its
// `>`. Those bytes are examined: never dropped, never copied unexamined.
func (r *redactor) finish(err error, start, end int) error {
	if err != io.EOF {
		return fmt.Errorf("%w: %v", ErrUnsafe, err)
	}
	if start < end {
		r.tail(start, end)
	}
	r.flush()
	if end != len(r.page) {
		return fmt.Errorf("%w: tokens covered %d of %d bytes", ErrUnsafe, end, len(r.page))
	}
	return nil
}

func (r *redactor) tail(start, end int) {
	switch {
	case r.page[start] == '<' && start+1 < end && isASCIILetter(r.page[start+1]):
		r.flush()
		r.tag(start, end, true)
	case r.page[start] == '<':
		r.flush()
		r.isolated(start, end, false)
	default:
		r.stream.decode(r.page, start, end, false)
	}
}

// text routes one Text token. Script-like content is raw — character
// references are NOT decoded there, per HTML — while textarea and title
// decode them. Both are read in isolation, so a secret inside cannot pair with
// prose outside.
func (r *redactor) text(start, end int, mode string) {
	switch mode {
	case "raw":
		r.flush()
		r.isolated(start, end, false)
	case "rcdata":
		r.flush()
		r.isolated(start, end, true)
	default:
		r.stream.decode(r.page, start, end, false)
	}
}

func (r *redactor) boundary(tag string) {
	if !inlineTags[tag] {
		r.flush()
	}
}

func (r *redactor) flush() {
	r.recognize(&r.stream, true)
	r.stream = mapped{}
}

func (r *redactor) isolated(start, end int, decode bool) {
	var m mapped
	if decode {
		m.decode(r.page, start, end, false)
	} else {
		m.identity(r.page, start, end)
	}
	r.recognize(&m, true)
}

// recognize runs every text-level detector over m and records what they find.
// withName is false where the text may be markup, so the username can never
// rewrite a tag or attribute name.
func (r *redactor) recognize(m *mapped, withName bool) {
	if m.text.Len() == 0 {
		return
	}
	s := m.text.String()
	emit := func(ds, de int) { r.edits.replaceAll(m.sourceOf(ds, de)) }
	scanQueryParams(s, emit)
	scanAssignments(s, emit)
	scanHeaders(s, emit)
	if withName {
		scanName(s, r.name, emit)
	}
}

// rawTextMode lists the elements whose content the tokenizer returns as text
// rather than markup, and whether character references inside are decoded
// ("rcdata") or not ("raw").
var rawTextMode = map[string]string{
	"script": "raw", "style": "raw", "xmp": "raw", "iframe": "raw",
	"noembed": "raw", "noframes": "raw", "noscript": "raw", "plaintext": "raw",
	"textarea": "rcdata", "title": "rcdata",
}

// inlineTags do not end a line of text. Anything else does, which keeps a
// value in one table cell from being read as continuing into the next.
var inlineTags = setOf("a", "abbr", "b", "bdi", "bdo", "big", "cite", "code",
	"data", "dfn", "em", "font", "i", "kbd", "mark", "nobr", "q", "s", "samp",
	"small", "span", "strike", "strong", "sub", "sup", "time", "tt", "u", "var")

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
