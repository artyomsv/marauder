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

// TestRedact_TokenInputSurvivesNoAttributeSpelling is the P1 Greptile raised
// on this package. The first version matched only `name="..."` appearing
// BEFORE `value="..."`, both double-quoted. HTML permits neither constraint,
// and a form token is as good as a session for anything that accepts it — so
// every spelling below leaked the reporter's credential into a file they were
// being invited to post publicly.
func TestRedact_TokenInputSurvivesNoAttributeSpelling(t *testing.T) {
	for _, tag := range []string{
		`<input type="hidden" name="form_token" value="s3cr3t" />`,
		`<input type="hidden" value="s3cr3t" name="form_token" />`,
		`<input type='hidden' name='form_token' value='s3cr3t'>`,
		`<input type='hidden' value='s3cr3t' name='form_token'>`,
		`<input type=hidden name=form_token value=s3cr3t>`,
		`<INPUT TYPE="HIDDEN" NAME="FORM_TOKEN" VALUE="s3cr3t">`,
		`<input name = "form_token"  value = "s3cr3t">`,
		`<input name="csrf-token" value="s3cr3t">`,
		`<input name="session_key" value="s3cr3t">`,
		// No type attribute at all: `type` is optional, so a rule keyed on
		// type="hidden" would pass this straight through.
		`<input name="form_token" value="s3cr3t">`,
	} {
		if got := string(Redact([]byte(tag), "")); strings.Contains(got, "s3cr3t") {
			t.Errorf("token survived in %q -> %q", tag, got)
		}
	}
}

// TestRedact_LeavesOrdinaryInputsAlone is the P2. Over-redaction destroys the
// markup the export exists to carry, and the first version blanked any input
// whose name merely CONTAINED a secret word as a substring — so `author`,
// `keywords` and `consideration` all matched (`auth`, `key`, `sid`).
func TestRedact_LeavesOrdinaryInputsAlone(t *testing.T) {
	for _, tag := range []string{
		`<input type="text" name="search" value="Stuart Fails">`,
		`<input type="text" name="author" value="Fire">`,
		`<input type="text" name="keywords" value="1080p">`,
		`<input type="hidden" name="consideration" value="12">`,
		`<input type="hidden" name="monkey" value="12">`,
		`<input type="submit" name="submit" value="Найти">`,
	} {
		if got := string(Redact([]byte(tag), "")); got != tag {
			t.Errorf("ordinary input was redacted:\n  in  %q\n  out %q", tag, got)
		}
	}
}

// TestRedact_PreservesTheValueDelimiter. Redaction must change the VALUE and
// nothing else — swapping single quotes for double, or adding them to an
// unquoted attribute, is a markup edit, and markup is the evidence.
func TestRedact_PreservesTheValueDelimiter(t *testing.T) {
	for in, want := range map[string]string{
		`<input name="form_token" value="x">`: `<input name="form_token" value="` + Placeholder + `">`,
		`<input name='form_token' value='x'>`: `<input name='form_token' value='` + Placeholder + `'>`,
		`<input name=form_token value=x>`:     `<input name=form_token value=` + Placeholder + `>`,
	} {
		if got := string(Redact([]byte(in), "")); got != want {
			t.Errorf("Redact(%q)\n  = %q\nwant %q", in, got, want)
		}
	}
}

// TestRedact_TokenInputWithAngleBracketInTheValue. A quoted attribute value
// may legally contain `>`, and a tag scan of `<input\b[^>]*>` stops at the
// first one it sees — truncating the tag before the value is reached, so the
// credential survived (Greptile P1, second round on #193).
//
// The unterminated-quote cases are here to pin the degradation: the pattern
// must fall back to the plain scan rather than failing to match the tag at
// all, because failing to match is failing open on a secret.
func TestRedact_TokenInputWithAngleBracketInTheValue(t *testing.T) {
	for _, tag := range []string{
		`<input type="hidden" name="form_token" value="s3cr3t>suffix">`,
		`<input type="hidden" value="s3cr3t>suffix" name="form_token">`,
		`<input name='form_token' value='s3cr3t>suffix'>`,
		`<input name="form_token" value="a>b>c">`,
		// Malformed: no closing quote. Must still not ship the token.
		`<input name="form_token" value="s3cr3t>`,
	} {
		if got := string(Redact([]byte(tag), "")); strings.Contains(got, "s3cr3t") {
			t.Errorf("token survived in %q -> %q", tag, got)
		}
	}
}

// TestRedact_AngleBracketInAnOrdinaryInputIsUntouched is the other direction:
// making the tag scan quote-aware must not start rewriting fields that carry
// no credential.
func TestRedact_AngleBracketInAnOrdinaryInputIsUntouched(t *testing.T) {
	const tag = `<input type="text" name="search" value="a>b">`
	if got := string(Redact([]byte(tag), "")); got != tag {
		t.Errorf("ordinary input was redacted:\n  in  %q\n  out %q", tag, got)
	}
}

// TestRedact_MalformedTagDoesNotShieldTheNextOne. Making the tag scan
// quote-aware let an UNTERMINATED quote pair with a quote in a later tag, so
// the two tags matched as one. attrValue then read the harmless first tag's
// name, the match was classified as ordinary, and the credential in the
// second tag rode out untouched (Greptile P1, third round on #193).
//
// A quoted run must therefore stop at a tag boundary: one malformed tag may
// cost its own value, never the next tag's.
func TestRedact_MalformedTagDoesNotShieldTheNextOne(t *testing.T) {
	for _, page := range []string{
		`<input type="text" name="search" value="unterminated>` +
			`<input type="hidden" name="form_token" value="s3cr3t">`,
		`<input name='broken value='1'>` +
			`<input name='form_token' value='s3cr3t'>`,
		// The malformed tag's own quote pairing with the NEXT tag's quote is
		// the exact mechanism; keep a plain tag between them too.
		`<input name="broken value="1">` +
			`<p>text</p>` +
			`<input name="session_key" value="s3cr3t">`,
	} {
		if got := string(Redact([]byte(page), "")); strings.Contains(got, "s3cr3t") {
			t.Errorf("a malformed tag shielded the credential after it:\n  in  %q\n  out %q", page, got)
		}
	}
}

// TestRedact_TruncatedPageStillRedactsTheLastTag. fetchPage caps a response
// at maxBodyBytes, so an oversized page arrives cut mid-tag with no closing
// `>`. A scan that requires one matches nothing there and ships whatever the
// final, half-written tag was carrying.
func TestRedact_TruncatedPageStillRedactsTheLastTag(t *testing.T) {
	for _, page := range []string{
		`<input type="hidden" name="form_token" value="s3cr3t"`,
		`<input type="hidden" name="form_token" value="s3cr3t`,
		`<p>ok</p><input name='session_key' value='s3cr3t'`,
	} {
		if got := string(Redact([]byte(page), "")); strings.Contains(got, "s3cr3t") {
			t.Errorf("token survived in a truncated page:\n  in  %q\n  out %q", page, got)
		}
	}
}
