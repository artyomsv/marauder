package pageredact

import (
	"bytes"
	"strings"
)

// attr is one attribute exactly as it appears in the source. Offsets are
// absolute positions in the page, and valStart..valEnd is the value's CONTENT
// — inside its quotes, if it has any — which is the only part ever replaced.
type attr struct {
	name, lower      string
	hasValue         bool
	valStart, valEnd int
}

// tagLex is one start tag read by the HTML attribute grammar. The tokenizer
// already bounds the tag; this reads inside it because the tokenizer reports
// decoded attribute values with no byte offsets, and silently drops a
// duplicated attribute that a public source file still carries.
type tagLex struct {
	name  string
	attrs []attr
	// dupe: an attribute name occurs more than once.
	dupe bool
	// odd: an attribute NAME contains a quote or `<`, which only happens when
	// a quote upstream went wrong and the tag swallowed something.
	odd bool
	// open: the page ended inside the tag.
	open bool
}

func (t tagLex) malformed() bool { return t.dupe || t.odd || t.open }

// lexTag reads the start tag beginning at page[start] (a `<`), stopping at its
// closing `>` or at limit.
func lexTag(page []byte, start, limit int) tagLex {
	var t tagLex
	i := start + 1
	for i < limit && !isSpace(page[i]) && page[i] != '/' && page[i] != '>' {
		i++
	}
	t.name = strings.ToLower(string(page[start+1 : i]))
	seen := map[string]bool{}
	for {
		for i < limit && (isSpace(page[i]) || page[i] == '/') {
			i++
		}
		if i >= limit {
			t.open = true
			return t
		}
		if page[i] == '>' {
			return t
		}
		var a attr
		a, i = lexAttr(page, i, limit)
		if seen[a.lower] {
			t.dupe = true
		}
		seen[a.lower] = true
		if strings.ContainsAny(a.name, `"'<`) {
			t.odd = true
		}
		t.attrs = append(t.attrs, a)
	}
}

// lexAttr reads one attribute starting at page[i] and returns it with the
// offset just past it. It follows the HTML tokenizer's states: a name runs to
// whitespace, `/`, `>` or `=` (its first character is kept even if it is
// `=`, so `@name` and `name` are different attributes); a value is
// double-quoted, single-quoted or unquoted; and an unterminated quote runs to
// limit rather than to some later quote in a different tag.
func lexAttr(page []byte, i, limit int) (attr, int) {
	ns := i
	i++
	for i < limit && !isSpace(page[i]) && page[i] != '/' && page[i] != '>' && page[i] != '=' {
		i++
	}
	a := attr{name: string(page[ns:i])}
	a.lower = strings.ToLower(a.name)

	j := i
	for j < limit && isSpace(page[j]) {
		j++
	}
	if j >= limit || page[j] != '=' {
		return a, i
	}
	j++
	for j < limit && isSpace(page[j]) {
		j++
	}
	a.hasValue = true
	if j >= limit {
		a.valStart, a.valEnd = j, j
		return a, j
	}
	switch q := page[j]; q {
	case '"', '\'':
		a.valStart = j + 1
		k := bytes.IndexByte(page[j+1:limit], q)
		if k < 0 {
			a.valEnd = limit
			return a, limit
		}
		a.valEnd = j + 1 + k
		return a, a.valEnd + 1
	case '>':
		a.valStart, a.valEnd = j, j
		return a, j
	default:
		k := j
		for k < limit && !isSpace(page[k]) && page[k] != '>' {
			k++
		}
		a.valStart, a.valEnd = j, k
		return a, k
	}
}

// isSpace is HTML's whitespace, which includes form feed.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// tag applies the element rules to one start tag, and a second, raw pass when
// the tag is malformed. open is set when the page ended inside it.
func (r *redactor) tag(start, end int, open bool) {
	t := lexTag(r.page, start, end)
	t.open = t.open || open
	r.element(t)
	if !t.malformed() {
		return
	}
	// A malformed tag may have swallowed the tags after it: an unterminated
	// quote in an ordinary field can run on into a credential field, merging
	// both into one "tag" whose effective name is the harmless one. Scan the
	// raw bytes for the text-level secrets, and read every `<letter` inside as
	// a tag of its own. One malformed tag may cost its own values; it must
	// never cost the next tag's.
	var m mapped
	m.identity(r.page, start, end)
	r.recognize(&m, false)

	// Each swallowed tag is lexed only up to the next `<letter`, so every byte
	// is read a bounded number of times however many there are. Lexing each
	// one to the end of the outer tag was quadratic — an unterminated quote
	// followed by thousands of `<x` took 42 s on a 48 KB page, synchronously
	// inside a request.
	var inner []int
	for i := start + 1; i < end-1; i++ {
		if r.page[i] == '<' && isASCIILetter(r.page[i+1]) {
			inner = append(inner, i)
		}
	}
	for k, at := range inner {
		limit := end
		if k+1 < len(inner) {
			limit = inner[k+1]
		}
		r.element(lexTag(r.page, at, limit))
	}
}

// element applies the per-attribute rules to one lexed tag.
func (r *redactor) element(t tagLex) {
	field := formFields[t.name] && r.anySecretName(t)
	for _, a := range t.attrs {
		if !a.hasValue || a.valStart >= a.valEnd {
			continue
		}
		switch {
		case field && (a.lower == "value" || a.lower == "content"):
			// <input name="form_token" value=…>, <meta name="csrf-token" content=…>
			r.edits.replace(span{a.valStart, a.valEnd})
		case isSecretName(a.name):
			// data-sid, data-auth_key, nonce: the attribute names its own value.
			r.edits.replace(span{a.valStart, a.valEnd})
		default:
			var m mapped
			m.decode(r.page, a.valStart, a.valEnd, true)
			r.recognize(&m, !structuralAttrs[a.lower])
		}
	}
}

// anySecretName reports whether any `name` attribute on the tag — decoded, so
// `form_to&#107;en` counts — names a credential. ANY rather than the first a
// browser would honour: see the package doc on ambiguity.
func (r *redactor) anySecretName(t tagLex) bool {
	for _, a := range t.attrs {
		if a.lower != "name" || !a.hasValue {
			continue
		}
		var m mapped
		m.decode(r.page, a.valStart, a.valEnd, true)
		if isSecretName(m.text.String()) {
			return true
		}
	}
	return false
}

// formFields are the elements whose `value` or `content` a `name` attribute
// describes. A custom element such as <input-widget> is not one of them:
// matching by prefix would rewrite markup that carries no field at all.
var formFields = setOf("input", "button", "select", "option", "textarea",
	"output", "param", "meta", "data", "keygen")

// structuralAttrs never have the username redacted from them. They describe
// the page's structure, not its content, and a username that happens to equal
// a CSS class — `seedmed` is the class that solved issue #186 — must not
// rewrite the evidence the export exists to carry. Credential detection still
// runs over them.
var structuralAttrs = setOf("class", "id", "name", "type", "rel", "for", "role",
	"style", "lang", "dir", "tabindex", "colspan", "rowspan", "width", "height",
	"align", "valign", "border", "cellpadding", "cellspacing", "bgcolor",
	"color", "size", "method", "target", "accesskey", "xmlns", "http-equiv",
	"charset", "media", "autocomplete")
