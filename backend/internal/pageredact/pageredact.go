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

// secretInputs matches a hidden form field whose NAME says it carries a
// token. A form token is as good as a session for anything that accepts it,
// and unlike a session id it travels in markup rather than in a URL.
//
// Deliberately narrow: it requires the name to contain one of these words, so
// an ordinary `<input name="search" value="...">` keeps its value. An
// over-eager rule would blank the page content that the report exists to
// show.
var secretInputs = regexp.MustCompile(
	`(?is)(<input\b[^>]*\bname="[^"]*(?:token|passkey|sid|auth|secret|session|key)[^"]*"[^>]*\bvalue=")([^"]*)(")`)

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
	s = secretInputs.ReplaceAllString(s, "${1}"+Placeholder+"${3}")
	s = cookieLines.ReplaceAllString(s, "${1}"+Placeholder)
	s = redactUsername(s, username)
	return []byte(s)
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
