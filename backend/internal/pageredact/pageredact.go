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
//   - No secret survives.
//   - Everything else is byte-identical. Tags, classes, attributes and text
//     are what the report is FOR; a redactor that reformats the page destroys
//     the evidence it was collected to preserve.
//
// Redaction is best-effort by nature: it removes what we know to be secret,
// on a public page a reporter could have viewed anyway. It is a seatbelt, not
// a guarantee, and the UI says so rather than promising the file is clean.
package pageredact

import (
	"regexp"
	"strings"
)

// Placeholder replaces every redacted value. It is deliberately loud and
// contains no characters that need HTML escaping, so a redacted page still
// renders and a reader can see that something was removed rather than
// wondering whether the tracker served an empty attribute.
const Placeholder = "MARAUDER-REDACTED"

// secretParams are query-string parameters whose value authenticates the
// caller. Matching the NAME rather than the value shape is what keeps this
// honest: a session id and a topic id are both hex, and only the name says
// which is which.
//
// `sid` is phpBB's session id and is the one that started this package. `uk`
// is the persistent-login key on the bb_data family of trackers — presenting
// it signs in WITHOUT the password. The rest are the usual names trackers use
// for per-account RSS and download keys.
var secretParams = regexp.MustCompile(
	`(?i)\b(sid|uk|passkey|pid|auth_key|authkey|apikey|api_key|token|access_token|secret|key)=[^"'&<>\s;]+`)

// inputTagRe finds every <input> tag. Deciding which of them carries a
// credential is done by reading the tag's ATTRIBUTES, not by a single pattern
// that also has to express attribute order and quoting.
//
// The single-pattern version was wrong in both directions at once. It required
// `name` before `value` and double quotes on both, so
// `<input value='x' name='form_token'>` — valid markup either way round, and
// what some templates emit — kept the credential and shipped it in a file the
// user was being invited to post publicly. And it matched the secret words as
// bare substrings, so `author`, `keywords`, `monkey` and `consideration` were
// all blanked (`auth`, `key`, `sid`), destroying the very markup the export
// exists to carry.
// The scan is quote-aware, and every alternative stops at `<`.
//
// Quote-aware because a quoted attribute value may legally contain `>`, and
// `<input\b[^>]*>` stops at the first one — truncating the tag before its
// value is reached, so `<input name="form_token" value="s3cr3t>x">` shipped
// the credential. The quoted alternatives come FIRST so a `>` inside quotes is
// part of the value.
//
// Bounded at `<` because the first quote-aware version was worse than the bug
// it fixed. An UNTERMINATED quote paired with a quote in a LATER tag, so two
// tags matched as one; attrValue then read the harmless first tag's name, the
// match was classified as ordinary, and a credential in the second tag rode
// out untouched. One malformed tag may cost its own value. It must never cost
// the next tag's.
//
// `[^><]` last also makes the pattern degrade to the plain scan inside a
// malformed tag rather than failing to match it at all — failing to match is
// failing open on a secret.
//
// The closing `>` is optional for the same reason. fetchPage caps a response
// at maxBodyBytes, so an oversized page arrives cut mid-tag; requiring the `>`
// would match nothing there and ship whatever the final half-written tag was
// carrying. It costs nothing on a well-formed tag: `[^><]` cannot eat a `>`,
// so the greedy run stops in front of it either way.
var inputTagRe = regexp.MustCompile(`(?is)<input\b(?:"[^"<]*"|'[^'<]*'|[^><])*>?`)

// attrRe reads one attribute. All three HTML spellings are accepted —
// double-quoted, single-quoted, and unquoted — because a redactor that only
// understands the tidy one fails open on a credential.
var attrRe = regexp.MustCompile(`(?is)\b([a-z_:][-\w:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)

// valueAttrRe locates the value attribute inside one tag so only its content
// is replaced. The delimiter is preserved (see redactValueAttr): swapping
// quotes or adding them to an unquoted attribute is a markup edit, and markup
// is the evidence.
// The last two alternatives catch an unterminated quote. They run to the end
// of the tag and so rewrite a little more than the value — which is a markup
// change, and normally forbidden here. It is allowed only on this branch
// because the alternative is shipping a credential: the tag was already
// malformed, and a mangled attribute in a diagnostic file costs less than the
// reporter's account.
var valueAttrRe = regexp.MustCompile(`(?is)\bvalue\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'>]+|"[^"]*|'[^']*)`)

// secretNameRe matches a field name that carries a credential. The words are
// matched at non-letter boundaries, not as substrings, which is what keeps
// `author`, `keywords`, `monkey` and `consideration` intact while still
// catching `form_token`, `csrf-token`, `session_key` and a bare `sid`.
//
// A form token is as good as a session for anything that accepts it, and
// unlike a session id it travels in markup rather than in a URL.
var secretNameRe = regexp.MustCompile(
	`(?i)(^|[^a-z])(sid|uk|csrf|xsrf|nonce|token|passkey|apikey|secret|session|key|auth|password|passwd|pwd)([^a-z]|$)`)

// cookieLines matches a Cookie header echoed into the page — some forum
// templates dump the request when debugging is left on. One line here is the
// whole session.
var cookieLines = regexp.MustCompile(`(?i)(Cookie:\s*)[^<\n\r]+`)

// Redact returns page with every known secret replaced by Placeholder.
//
// username is the reporter's own tracker account name, redacted so a bug
// report does not out which account they use; the page prints it in the
// header bar. It is optional — credential warming degrades to nil on any
// failure — and an empty string redacts no name rather than matching
// everywhere.
//
// The returned slice is a new allocation; page is not modified.
func Redact(page []byte, username string) []byte {
	if len(page) == 0 {
		return page
	}
	s := string(page)
	s = secretParams.ReplaceAllStringFunc(s, func(m string) string {
		name, _, _ := strings.Cut(m, "=")
		return name + "=" + Placeholder
	})
	s = redactInputs(s)
	s = cookieLines.ReplaceAllString(s, "${1}"+Placeholder)
	s = redactUsername(s, username)
	return []byte(s)
}

// redactInputs blanks the value of every <input> whose name carries a
// credential, leaving every other input — and the rest of the tag — untouched.
//
// It deliberately does NOT require type="hidden". The attribute is optional,
// a template is free to carry a token in a visible field, and the cost of the
// two mistakes is not symmetric: a needlessly blanked value loses one
// attribute from a diagnostic file, while a missed one hands the reporter's
// account to everyone who reads the bug report.
func redactInputs(s string) string {
	return inputTagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if !secretNameRe.MatchString(attrValue(tag, "name")) {
			return tag
		}
		return valueAttrRe.ReplaceAllStringFunc(tag, redactValueAttr)
	})
}

// attrValue returns the named attribute's value from one tag, or "".
func attrValue(tag, want string) string {
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		if !strings.EqualFold(m[1], want) {
			continue
		}
		for _, v := range m[2:] {
			if v != "" {
				return v
			}
		}
		return ""
	}
	return ""
}

// redactValueAttr replaces one `value=...` attribute's content, keeping its
// delimiter and the spacing around the `=` exactly as the page wrote them.
func redactValueAttr(attr string) string {
	eq := strings.Index(attr, "=")
	if eq < 0 {
		return attr
	}
	head, rest := attr[:eq+1], attr[eq+1:]
	trimmed := strings.TrimLeft(rest, " \t\r\n")
	pad := rest[:len(rest)-len(trimmed)]
	switch {
	case strings.HasPrefix(trimmed, `"`):
		return head + pad + `"` + Placeholder + `"`
	case strings.HasPrefix(trimmed, `'`):
		return head + pad + `'` + Placeholder + `'`
	default:
		return head + pad + Placeholder
	}
}

// redactUsername replaces whole-word occurrences of name, case-insensitively.
//
// Whole-word is the point: a substring replace on a short account name would
// corrupt unrelated text all over the page, and a corrupted page is a useless
// report. regexp.QuoteMeta because an account name is user input and may
// contain regex metacharacters.
func redactUsername(s, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return s
	}
	re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		// A name that will not compile even quoted is not worth failing the
		// whole redaction over — every other rule has already run.
		return s
	}
	return re.ReplaceAllString(s, Placeholder)
}
