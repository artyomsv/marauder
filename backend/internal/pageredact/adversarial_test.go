package pageredact

import "testing"

// These cases come from an independent adversarial review of PR #193 (F1-F10).
// Every input and expected output is the reviewer's, reproduced against the
// regex-based redactor this package replaced. They are exact-output tests on
// purpose: the contract has two halves — no secret survives, and every other
// byte is untouched — and a test that only checks for the absence of the
// secret cannot catch the second half, which is where most of these failed.

const ph = Placeholder

type exact struct {
	name, user, in, want string
}

func runExact(t *testing.T, cases []exact) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact(t, tc.in, tc.user); got != tc.want {
				t.Errorf("\n  in   %q\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}

// F1 (P1). Stopping a quoted attribute at `<` — the previous round's fix for a
// tag-spanning leak — broke valid markup: the scan never reached the real
// name or value, and a `<` inside a secret leaked its tail.
func TestReview_F1_AngleBracketInsideAValidQuotedValue(t *testing.T) {
	runExact(t, []exact{
		{"lt in an earlier attribute", "",
			`<input title="1 < 2" name="form_token" value="s3cr3t">`,
			`<input title="1 < 2" name="form_token" value="` + ph + `">`},
		{"lt inside the secret itself", "",
			`<input name="form_token" value="abc<s3cr3t">`,
			`<input name="form_token" value="` + ph + `">`},
	})
}

// F2 (P1). A word boundary is not an HTML attribute boundary: `@name` is its
// own attribute, and the old pattern read it as `name`.
//
// The duplicate-name case is a DELIBERATE divergence from browser semantics,
// which honour the first `name` (empty here) and would leave the value alone.
// A public source file still carries the later `name="form_token"` and its
// value; this package redacts when ANY name attribute on a tag is a credential
// name, because a template emitting duplicates is exactly the template that
// cannot be trusted to have put the secret where the browser looks.
func TestReview_F2_AttributeNamesAreLexedNotSubstringMatched(t *testing.T) {
	runExact(t, []exact{
		{"at-prefixed attribute is not name", "",
			`<input @name="search" name="form_token" value="s3cr3t">`,
			`<input @name="search" name="form_token" value="` + ph + `">`},
		{"duplicate name, conservative by design", "",
			`<input name name="form_token" value="ordinary">`,
			`<input name name="form_token" value="` + ph + `">`},
	})
}

// F3 (P1). Character references hide a credential name or value from a
// pattern that reads raw bytes. Classification now happens on decoded text,
// while the replacement is mapped back onto the ORIGINAL bytes — the encoded
// spelling of everything that is not secret survives untouched.
func TestReview_F3_CharacterReferences(t *testing.T) {
	runExact(t, []exact{
		{"encoded field name", "",
			`<input name="form_to&#107;en" value="s3cr3t">`,
			`<input name="form_to&#107;en" value="` + ph + `">`},
		{"encoded query key", "",
			`<a href="/?s&#105;d=s3cr3t">x</a>`,
			`<a href="/?s&#105;d=` + ph + `">x</a>`},
		{"fully encoded query value", "",
			`<a href="/?sid=&#115;&#51;cr3t">x</a>`,
			`<a href="/?sid=` + ph + `">x</a>`},
		{"partly encoded query value", "",
			`<a href="/?sid=abc&#100;ef">x</a>`,
			`<a href="/?sid=` + ph + `">x</a>`},
	})
}

// F4 (P1). A credential does not have to live in a URL or an <input>.
func TestReview_F4_CredentialsInDataAttributesAndScripts(t *testing.T) {
	runExact(t, []exact{
		{"data-sid", "",
			`<div data-sid="s3cr3t"></div>`,
			`<div data-sid="` + ph + `"></div>`},
		{"data-auth_key", "",
			`<div data-auth_key="s3cr3t"></div>`,
			`<div data-auth_key="` + ph + `"></div>`},
		{"js assignment", "",
			`<script>sid="s3cr3t";</script>`,
			`<script>sid="` + ph + `";</script>`},
		{"json object", "",
			`<script>const c={"sid":"s3cr3t","auth_key":"k3y"};</script>`,
			`<script>const c={"sid":"` + ph + `","auth_key":"` + ph + `"};</script>`},
	})
}

// F5 (P1). An echoed Cookie line must be found when its value is wrapped in
// formatting markup, and must stop at the end of its own line or attribute —
// the old pattern ran through the closing quote and ate the rest of the tag.
func TestReview_F5_EchoedCookieLines(t *testing.T) {
	runExact(t, []exact{
		{"value wrapped in inline markup", "",
			`<pre>Cookie: <b>session=s3cr3t</b></pre>`,
			`<pre>Cookie: <b>` + ph + `</b></pre>`},
		{"inside an attribute, stops at its end", "",
			`<div title="Cookie: session=s3cr3t" class="seedmed">ok</div>`,
			`<div title="Cookie: ` + ph + `" class="seedmed">ok</div>`},
		{"empty header does not eat the next line", "",
			"<pre>Cookie:\nno cookie on this line</pre>",
			"<pre>Cookie:\nno cookie on this line</pre>"},
	})
}

// F6 (P2). Only the attribute actually named `value` is rewritten, and only
// its content: another attribute containing the word, and the whitespace and
// quotes around the value, are evidence and stay byte-for-byte.
func TestReview_F6_OnlyTheValueAttributeContentChanges(t *testing.T) {
	runExact(t, []exact{
		{"data-value is not value", "",
			`<input name="form_token" data-value="ordinary" value="s3cr3t">`,
			`<input name="form_token" data-value="ordinary" value="` + ph + `">`},
		{"value= inside another attribute", "",
			`<input name="form_token" title='value="ordinary"' value="s3cr3t">`,
			`<input name="form_token" title='value="ordinary"' value="` + ph + `">`},
		{"form-feed whitespace and quotes preserved", "",
			"<INPUT\fNAME\f=\f'FORM_TOKEN'\fVALUE\f=\f's3cr3t'>",
			"<INPUT\fNAME\f=\f'FORM_TOKEN'\fVALUE\f=\f'" + ph + "'>"},
	})
}

// F7 (P2). Text that merely LOOKS like an input element is not one. A custom
// element with a longer name, markup quoted inside a value, and example markup
// in a comment, a textarea or CDATA all describe no real field and must be
// left exactly as written.
func TestReview_F7_TextThatLooksLikeAnInputIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`<input-widget name="form_token" value="ordinary"></input-widget>`,
		`<input name="search" value='<input name="form_token" value="ordinary">'>`,
		`<!-- example: <input name="form_token" value="ordinary"> -->`,
		`<textarea><input name="form_token" value="ordinary"></textarea>`,
		`<svg><![CDATA[<input name="form_token" value="ordinary">]]></svg>`,
	} {
		if got := redact(t, in, ""); got != in {
			t.Errorf("rewrote markup that contains no real field:\n  in  %q\n  got %q", in, got)
		}
	}
}

// F8 (P2). The reporter's username must be found when it is not a plain ASCII
// word: bracketed, non-Latin, or HTML-escaped in the source.
func TestReview_F8_UsernamesThatAreNotASCIIWords(t *testing.T) {
	runExact(t, []exact{
		{"brackets at both edges", `[alice]`,
			`<span>[alice]</span>`, `<span>` + ph + `</span>`},
		{"cyrillic", `Иван`,
			`<span>Иван</span>`, `<span>` + ph + `</span>`},
		{"html-escaped in the source", `alice&bob`,
			`<span>alice&amp;bob</span>`, `<span>` + ph + `</span>`},
	})
}

// F9 (P2). A username that collides with markup must not rewrite the markup.
// `seedmed` is the CSS class that solved issue #186 — redacting it would
// destroy the exact evidence this feature exists to collect.
func TestReview_F9_UsernameNeverRewritesMarkup(t *testing.T) {
	runExact(t, []exact{
		{"class attribute survives", `seedmed`,
			`<th class="seedmed">release.torrent</th><b>seedmed</b>`,
			`<th class="seedmed">release.torrent</th><b>` + ph + `</b>`},
		{"tag name survives", `input`,
			`<input name="form_token" value="s3cr3t"><span>input</span>`,
			`<input name="form_token" value="` + ph + `"><span>` + ph + `</span>`},
		{"placeholder is not re-matched", `REDACTED`,
			`<a href="/?sid=s3cr3t">REDACTED</a>`,
			`<a href="/?sid=` + ph + `">` + ph + `</a>`},
	})
}

// F9, second half: a second pass over already-redacted output must change
// nothing, including when the username is a substring of the placeholder.
func TestReview_F9_RedactionIsIdempotent(t *testing.T) {
	const in = `<a href="/?sid=s3cr3t">REDACTED</a>`
	once := redact(t, in, "REDACTED")
	if twice := redact(t, once, "REDACTED"); twice != once {
		t.Errorf("second pass changed the output:\n  once  %q\n  twice %q", once, twice)
	}
}

// F10 (P2). A URL fragment is not part of a query value.
func TestReview_F10_FragmentSurvives(t *testing.T) {
	runExact(t, []exact{
		{"fragment after a secret param", "",
			`<a href="/?sid=s3cr3t#comments">x</a>`,
			`<a href="/?sid=` + ph + `#comments">x</a>`},
	})
}
