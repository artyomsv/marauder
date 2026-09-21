package pageredact

import (
	"strings"
	"testing"
)

// livePageFragment is real markup from tapochek.net, captured 2026-09-21
// while debugging issue #186. The sid is the actual one the page carried and
// is the reason this package exists: without redaction, the "copy the page
// HTML" button would invite users to paste a live session id into a public
// bug report. It is dead now (the session was ended), and no test may ever
// replace it with a live one.
const livePageFragment = `<p class="small"><a href="viewtopic.php?t=288010&amp;watch=topic&amp;start=0&amp;sid=wCll0mxQk34M71ITmQdA">Следить за ответами</a></p>
<table class="attach bordered med">
	<tr class="row3">
		<th colspan="3" class="seedmed">Some.Release.[tapochek.net].torrent</th>
	</tr>
	<tr class="row1">
		<td width="15%">Размер:</td>
		<td>13.03&nbsp;GB</td>
	</tr>
</table>`

func TestRedact_RemovesTheSessionID(t *testing.T) {
	got := string(Redact([]byte(livePageFragment), ""))
	if strings.Contains(got, "wCll0mxQk34M71ITmQdA") {
		t.Fatal("the session id survived redaction")
	}
	if !strings.Contains(got, "sid="+Placeholder) {
		t.Errorf("sid was removed but not marked; got %q", got)
	}
}

// TestRedact_KeepsTheMarkup is the other half of the contract. Redaction that
// disturbs tags, classes or structure destroys the only thing the page was
// collected for — issue #186 was solved by one class attribute.
func TestRedact_KeepsTheMarkup(t *testing.T) {
	got := string(Redact([]byte(livePageFragment), ""))
	for _, want := range []string{
		`<table class="attach bordered med">`,
		`<th colspan="3" class="seedmed">Some.Release.[tapochek.net].torrent</th>`,
		`<td width="15%">Размер:</td>`,
		`13.03&nbsp;GB`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction removed markup we need: %q", want)
		}
	}
}

func TestRedact_RemovesCredentialParameters(t *testing.T) {
	for _, tc := range []struct{ name, in, leak string }{
		{"passkey query", `<a href="/download.php?id=1&passkey=abcdef0123456789">dl</a>`, "abcdef0123456789"},
		{"uk autologin", `<a href="/index.php?uk=Zm9vYmFyYmF6">x</a>`, "Zm9vYmFyYmF6"},
		{"auth key", `<a href="/rss.php?auth_key=deadbeefcafe">rss</a>`, "deadbeefcafe"},
		{"api token", `<img src="/pic.php?token=t0p53cr3t" />`, "t0p53cr3t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(Redact([]byte(tc.in), ""))
			if strings.Contains(got, tc.leak) {
				t.Errorf("%s survived: %q", tc.leak, got)
			}
		})
	}
}

// TestRedact_RemovesHiddenTokenInputs. A form token is as good as a session
// for anything that accepts it, and it travels in markup rather than a URL.
func TestRedact_RemovesHiddenTokenInputs(t *testing.T) {
	in := `<input type="hidden" name="form_token" value="9f8e7d6c" />
<input type="hidden" name="creation_time" value="1758400000" />
<input type="hidden" name="session_key" value="abc123" />
<input type="text" name="search" value="Stuart Fails" />`
	got := string(Redact([]byte(in), ""))
	for _, leak := range []string{"9f8e7d6c", "abc123"} {
		if strings.Contains(got, leak) {
			t.Errorf("token value %q survived: %q", leak, got)
		}
	}
	// A non-secret input must be left alone: over-redaction hides the markup
	// differences these reports exist to show.
	if !strings.Contains(got, `value="Stuart Fails"`) {
		t.Errorf("an ordinary input was redacted: %q", got)
	}
}

// TestRedact_RemovesTheReportersUsername. A bug report should not out which
// tracker account the reporter uses; the page prints it in the header bar.
func TestRedact_RemovesTheReportersUsername(t *testing.T) {
	in := `<span class="userName">odcold</span> · <a href="profile.php?u=1">ODCOLD</a> · odcoldish`
	got := string(Redact([]byte(in), "odcold"))
	if strings.Contains(got, ">odcold<") || strings.Contains(got, ">ODCOLD<") {
		t.Errorf("username survived: %q", got)
	}
	if n := strings.Count(got, Placeholder); n != 2 {
		t.Errorf("redacted %d occurrences, want 2 (both cases): %q", n, got)
	}
	// The match is case-insensitive but must not be a substring free-for-all:
	// "odcoldish" is a different WORD, and silently rewriting it would corrupt
	// exactly the page text the report exists to show.
	if !strings.Contains(got, "odcoldish") {
		t.Errorf("redaction ate a different word that merely starts the same: %q", got)
	}
}

// TestRedact_EmptyUsernameIsNotAWildcard. Warm() degrades to nil credentials
// on any failure, so the handler can legitimately have no username to pass. An
// empty needle must redact nothing rather than match at every position.
func TestRedact_EmptyUsernameIsNotAWildcard(t *testing.T) {
	const in = `<p>plain page</p>`
	if got := string(Redact([]byte(in), "")); got != in {
		t.Errorf("empty username changed the page: %q", got)
	}
}

// TestRedact_RemovesCookieHeadersEchoedIntoThePage. Some forum templates dump
// the request for debugging; a Cookie line there carries the whole session.
func TestRedact_RemovesCookieHeadersEchoedIntoThePage(t *testing.T) {
	in := `<pre>Cookie: bb_data=a%3A3%3A%7Bs%3A2%3A%22uk%22%3B; other=1</pre>`
	got := string(Redact([]byte(in), ""))
	if strings.Contains(got, "bb_data=a%3A3") {
		t.Errorf("cookie value survived: %q", got)
	}
}

// TestRedact_IsIdempotent. The UI may redact, and a user may run the result
// through again; a second pass must not mangle the placeholders.
func TestRedact_IsIdempotent(t *testing.T) {
	once := Redact([]byte(livePageFragment), "someone")
	twice := Redact(once, "someone")
	if string(once) != string(twice) {
		t.Errorf("second pass changed the output:\n%s\n---\n%s", once, twice)
	}
}

func TestRedact_NilAndEmptyInput(t *testing.T) {
	if got := Redact(nil, "x"); got != nil {
		t.Errorf("Redact(nil) = %q, want nil", got)
	}
	if got := Redact([]byte{}, "x"); len(got) != 0 {
		t.Errorf("Redact(empty) = %q, want empty", got)
	}
}
