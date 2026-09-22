package pageredact

import (
	"strings"
	"testing"
	"time"
)

// These pin the two quadratic paths an independent review measured in the
// rebuilt redactor (N7, N13). Both bounds are far above linear run time even
// under -race, and far below what the quadratic versions needed at these
// sizes, so neither can pass by luck in one direction or flake in the other.

// TestScaling_MalformedTagWithManySwallowedTags is N7. An unterminated quote
// followed by thousands of `<x` made recovery re-lex the rest of the tag once
// per `<x`: 48 KB took 39 s. A tracker page can legitimately be that broken,
// and Redact runs synchronously inside a request.
func TestScaling_MalformedTagWithManySwallowedTags(t *testing.T) {
	page := "<div a=\"" + strings.Repeat("<x ", 16_000)
	start := time.Now()
	out, err := Redact([]byte(page), "")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("48 KB malformed tag took %v; recovery is not linear", elapsed)
	}
	if string(out) != page {
		t.Error("an ordinary malformed tag was rewritten")
	}
}

// TestScaling_PlaceholderAbsorption is N13. Absorbing existing placeholders
// compared every placeholder with every edit; a second pass over an already
// redacted 3.8 MB page took 17.5 s. This drives the edit list directly so the
// bound measures the algorithm, not the tokenizer.
func TestScaling_PlaceholderAbsorption(t *testing.T) {
	const n = 200_000
	unit := "<p>" + Placeholder + " alice</p>"
	page := []byte(strings.Repeat(unit, n))
	var l editList
	for i := 0; i < n; i++ {
		at := i*len(unit) + len("<p>")
		l.replace(span{at + 9, at + 17})                                      // inside the placeholder
		l.replace(span{at + len(Placeholder) + 1, at + len(Placeholder) + 6}) // "alice"
	}
	start := time.Now()
	out := l.apply(page)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("%d placeholders x %d edits took %v; absorption is not linear", n, 2*n, elapsed)
	}
	if want := strings.Repeat("<p>"+Placeholder+" "+Placeholder+"</p>", n); string(out) != want {
		t.Error("absorption changed the output")
	}
}
