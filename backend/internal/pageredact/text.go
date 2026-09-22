package pageredact

import (
	"bytes"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// run ties a stretch of decoded text to the source bytes it came from. An
// identity run maps byte for byte. A reference run maps every decoded byte of
// one character reference (`&#107;`, `&amp;`) to the WHOLE reference, so a
// secret that begins or ends inside one is replaced in full rather than
// leaving half an entity behind.
type run struct {
	dec, n   int // decoded [dec, dec+n)
	src, end int // source [src, end)
	ident    bool
}

// mapped is decoded text that remembers where every byte came from.
type mapped struct {
	text strings.Builder
	runs []run
}

func (m *mapped) identity(page []byte, start, end int) {
	if start >= end {
		return
	}
	m.runs = append(m.runs, run{dec: m.text.Len(), n: end - start, src: start, end: end, ident: true})
	m.text.Write(page[start:end])
}

func (m *mapped) reference(decoded string, start, end int) {
	if decoded == "" {
		return
	}
	m.runs = append(m.runs, run{dec: m.text.Len(), n: len(decoded), src: start, end: end})
	m.text.WriteString(decoded)
}

var charRefRe = regexp.MustCompile(`^&(?:#[0-9]{1,8};?|#[xX][0-9a-fA-F]{1,8};?|[A-Za-z][A-Za-z0-9]{1,31};?)`)

// decode appends page[start:end] to m with character references resolved.
//
// inAttr applies HTML's attribute-value rule: a named reference with no
// closing `;` stays literal, so `?a=1&copy=2` remains a query string instead
// of becoming `?a=1©=2` and moving every parameter boundary after it.
func (m *mapped) decode(page []byte, start, end int, inAttr bool) {
	lit := start
	for i := start; i < end; {
		j := bytes.IndexByte(page[i:end], '&')
		if j < 0 {
			break
		}
		i += j
		loc := charRefRe.FindIndex(page[i:end])
		if loc == nil {
			i++
			continue
		}
		ref := string(page[i : i+loc[1]])
		next := i + loc[1]
		if inAttr && ref[1] != '#' && !strings.HasSuffix(ref, ";") {
			i = next
			continue
		}
		dec := html.UnescapeString(ref)
		if dec == ref {
			i = next
			continue
		}
		m.identity(page, lit, i)
		m.reference(dec, i, next)
		i, lit = next, next
	}
	m.identity(page, lit, end)
}

// sourceOf maps decoded [ds, de) back to the source pieces it came from, in
// order. A secret split by inline markup maps to more than one piece; the
// markup between them belongs to neither and is left alone.
func (m *mapped) sourceOf(ds, de int) []span {
	i := sort.Search(len(m.runs), func(k int) bool { return m.runs[k].dec+m.runs[k].n > ds })
	var out []span
	for ; i < len(m.runs) && m.runs[i].dec < de; i++ {
		r := m.runs[i]
		lo, hi := max(ds, r.dec), min(de, r.dec+r.n)
		if lo >= hi {
			continue
		}
		s := span{r.src, r.end}
		if r.ident {
			s = span{r.src + lo - r.dec, r.src + hi - r.dec}
		}
		if n := len(out); n > 0 && out[n-1].end >= s.start {
			out[n-1].end = max(out[n-1].end, s.end)
		} else {
			out = append(out, s)
		}
	}
	return out
}

// --- what counts as a credential -------------------------------------------

// secretWords mark a name, key or parameter as carrying a credential. `sid`
// is phpBB's session id, the one that started this package; `uk` is the
// persistent-login key on the bb_data family of trackers, which signs in
// WITHOUT the password.
var secretWords = setOf("sid", "uk", "csrf", "xsrf", "nonce", "token",
	"passkey", "apikey", "authkey", "secret", "session", "sessionid", "sessid",
	"phpsessid", "key", "auth", "password", "passwd", "pwd")

// isSecretName reports whether name contains one of secretWords as a WORD.
// Words split at every non-alphanumeric character and at each lower-to-upper
// case change, so `form_token`, `csrf-token`, `authToken` and `apiKey`
// qualify while `author`, `keywords`, `monkey` and `consideration` — which
// merely contain one as a substring — do not.
func isSecretName(name string) bool {
	for _, w := range words(name) {
		if secretWords[strings.ToLower(w)] {
			return true
		}
	}
	return false
}

func isSecretParam(key string) bool {
	return isSecretName(key) || strings.EqualFold(key, "pid")
}

func words(s string) []string {
	var out []string
	start := -1
	var prev rune
	for i, r := range s {
		alnum := unicode.IsLetter(r) || unicode.IsDigit(r)
		switch {
		case !alnum:
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		case start < 0:
			start = i
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			out = append(out, s[start:i])
			start = i
		}
		prev = r
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// --- detectors -------------------------------------------------------------
//
// Each detector reads decoded text and reports the DECODED span of a secret
// value. Every one is linear: a key is bounded to maxKey bytes, and scanning
// resumes after a value it has reported, so an adversarial page cannot make a
// value be re-scanned once per occurrence of its key.

const maxKey = 64

func isKeyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.' || c == '[' || c == ']'
}

func isQueryValueByte(c byte) bool {
	return !isSpace(c) && strings.IndexByte("\"'&<>;#", c) < 0
}

// scanQueryParams finds `key=value` where the key is a credential: URLs,
// form-encoded bodies, or bare `sid=…` in text. The value stops at `&`, `;`,
// `#` (a fragment is not part of it), a quote, `<`, `>` or whitespace.
func scanQueryParams(s string, emit func(int, int)) {
	for p := 0; p < len(s); p++ {
		if s[p] != '=' {
			continue
		}
		k := p
		for k > 0 && p-k < maxKey && isKeyByte(s[k-1]) {
			k--
		}
		if k == p || !isSecretParam(s[k:p]) {
			continue // not consumed: `href=/x?sid=…` must still find sid
		}
		e := p + 1
		for e < len(s) && isQueryValueByte(s[e]) {
			e++
		}
		if e > p+1 {
			emit(p+1, e)
			p = e - 1
		}
	}
}

func isIdentByte(c byte) bool {
	return isKeyByte(c) && c != '[' && c != ']' || c == '$'
}

// scanAssignments finds `key: "value"` and `key = "value"` — JavaScript
// assignments, object literals and JSON — where the key is a credential, and
// reports the quoted value's content.
func scanAssignments(s string, emit func(int, int)) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != ':' && c != '=' {
			continue
		}
		if c == '=' && (i+1 < len(s) && s[i+1] == '=' || i > 0 && strings.IndexByte("=!<>", s[i-1]) >= 0) {
			continue // a comparison, not an assignment
		}
		key, ok := keyBefore(s, i)
		if !ok || !isSecretName(key) {
			continue
		}
		v := i + 1
		for v < len(s) && (s[v] == ' ' || s[v] == '\t') {
			v++
		}
		if v >= len(s) || s[v] != '"' && s[v] != '\'' {
			continue
		}
		if end, ok := quotedEnd(s, v); ok {
			emit(v+1, end)
			i = end
		}
	}
}

// keyBefore returns the key that ends just before s[i], skipping blanks: a
// quoted string or a run of identifier characters, either bounded to maxKey.
func keyBefore(s string, i int) (string, bool) {
	j := i
	for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	if j == 0 {
		return "", false
	}
	if q := s[j-1]; q == '"' || q == '\'' {
		from := max(0, j-2-maxKey)
		k := strings.LastIndexByte(s[from:j-1], q)
		if k < 0 {
			return "", false
		}
		return s[from+k+1 : j-1], true
	}
	k := j
	for k > 0 && j-k < maxKey && isIdentByte(s[k-1]) {
		k--
	}
	return s[k:j], k < j
}

// quotedEnd returns the index of the quote closing the string that opens at
// s[v], honouring backslash escapes. A string does not continue past a line
// break, so an unbalanced quote cannot claim the rest of the page.
func quotedEnd(s string, v int) (int, bool) {
	q := s[v]
	for i := v + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '\n', '\r':
			return 0, false
		case q:
			return i, true
		}
	}
	return 0, false
}

var headerRe = regexp.MustCompile(`(?i)(?:set-cookie|cookie|authorization)[ \t]*:[ \t]*`)

// scanHeaders finds an echoed Cookie, Set-Cookie or Authorization header —
// some forum templates dump the request when debugging is left on — and
// reports the rest of its line. The value stops at a line break and at the end
// of the text it was found in, so inside an attribute it cannot run past the
// closing quote, and an empty header does not claim the next line.
func scanHeaders(s string, emit func(int, int)) {
	for pos := 0; pos < len(s); {
		loc := headerRe.FindStringIndex(s[pos:])
		if loc == nil {
			return
		}
		h, v := pos+loc[0], pos+loc[1]
		pos = v
		if h > 0 && isWordByte(s[h-1]) {
			continue
		}
		e := v
		for e < len(s) && s[e] != '\n' && s[e] != '\r' {
			e++
		}
		for e > v && (s[e-1] == ' ' || s[e-1] == '\t') {
			e--
		}
		if e > v {
			emit(v, e)
			pos = e
		}
	}
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// scanName finds the reporter's username, case-insensitively.
//
// Boundaries are checked the way a reader sees a word, in Unicode: an edge of
// the name that is itself a letter or digit must not continue into another
// letter or digit (`odcold` must not match inside `odcoldish`), while an edge
// that is punctuation needs no boundary at all. `\b` could do neither — it is
// ASCII-only, so it never delimited `Иван`, and it demanded a word character
// next to `[` in `[alice]`.
func scanName(s, name string, emit func(int, int)) {
	if name == "" {
		return
	}
	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	needLeft, needRight := isWordRune(first), isWordRune(last)
	for i := 0; i < len(s); {
		if n := foldPrefix(s[i:], name); n > 0 {
			left := !needLeft || i == 0 || !isWordRune(lastRune(s[:i]))
			right := !needRight || i+n == len(s) || !isWordRune(firstRune(s[i+n:]))
			if left && right {
				emit(i, i+n)
				i += n
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
}

// foldPrefix returns how many bytes of s match prefix under Unicode case
// folding, or 0 when they do not.
func foldPrefix(s, prefix string) int {
	i := 0
	for _, pr := range prefix {
		if i >= len(s) {
			return 0
		}
		sr, size := utf8.DecodeRuneInString(s[i:])
		if sr != pr && !equalFold(sr, pr) {
			return 0
		}
		i += size
	}
	return i
}

func equalFold(a, b rune) bool {
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}
