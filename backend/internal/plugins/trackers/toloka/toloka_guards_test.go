package toloka

// Tests closing the branches a mutation sweep found unprotected: each one
// below fails if the guard it names is deleted. They live in their own file
// so the main test file stays focused on parsing the captured markup.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

// TestSearch_ExpiredSession_ReportsTheRealReason covers the SECOND half of
// Search's credential handling: credentials exist, but the session behind
// them is dead. tracker.php answers a guest with the form, zero rows and no
// error, so "no results" here would tell the user their query matched nothing
// when in fact they are logged out.
func TestSearch_ExpiredSession_ReportsTheRealReason(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: guestCookie, Path: "/"})
		_, _ = w.Write([]byte(`<html><body><table></table></body></html>`))
	})
	_, err := p.Search(context.Background(), "anything", testCreds())
	if !errors.Is(err, registry.ErrSearchRequiresCredentials) {
		t.Errorf("error = %v, want registry.ErrSearchRequiresCredentials", err)
	}
}

// TestLogin_UsesAFreshJar: posting a password onto an already-authenticated
// session proves nothing about the password, because the server would answer
// from the session it already trusts. Login must validate on an unstored jar
// and publish it only once authenticated — the pattern SessionStore's own
// docs call for.
func TestLogin_UsesAFreshJar(t *testing.T) {
	registry.SetDomainResolver(nil)
	var mu sync.Mutex
	var postCookies []string
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		if r.Method == http.MethodPost {
			mu.Lock()
			postCookies = append(postCookies, r.Header.Get("Cookie"))
			mu.Unlock()
			http.Redirect(w, r, "/index.php", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html><body>index</body></html>"))
	})
	creds := testCreds()
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("first Login: %v", err)
	}
	// The stored session is authenticated now. A second Login must still post
	// on a clean jar, carrying no session cookie.
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("second Login: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(postCookies) != 2 {
		t.Fatalf("saw %d login POSTs, want 2", len(postCookies))
	}
	if strings.Contains(postCookies[1], sessionCookie) {
		t.Errorf("the second Login posted on an authenticated jar (%q) — a wrong password would then look valid", postCookies[1])
	}
}

// TestSession_ConcurrentUseIsRaceFree drives several goroutines through one
// shared session, which is what the scheduler's worker pool does across a
// user's topics. It exists because -race cannot flag a write that nothing
// else reads concurrently: configuring the client after the store published
// it only races once a second worker is already inside Client.Do.
func TestSession_ConcurrentUseIsRaceFree(t *testing.T) {
	registry.SetDomainResolver(nil)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(fixtureTopicHTML))
	})
	creds := testCreds()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.fetch(context.Background(), "https://toloka.to/t699998", creds); err != nil {
				t.Errorf("concurrent fetch: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCanParse_CustomDomain_AllowedViaResolver(t *testing.T) {
	registry.SetDomainResolver(func(name string) registry.DomainConfig {
		if name == pluginName {
			return registry.DomainConfig{Custom: []string{"toloka.example"}}
		}
		return registry.DomainConfig{}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{}
	if !p.CanParse("https://toloka.example/t123") {
		t.Error("an admin-configured custom mirror should parse")
	}
	if p.CanParse("https://evil.example/t123") {
		t.Error("an unlisted domain must not parse")
	}
}

func TestEffectiveDomain_ResolverOverride(t *testing.T) {
	registry.SetDomainResolver(func(string) registry.DomainConfig {
		return registry.DomainConfig{Active: "toloka.example"}
	})
	t.Cleanup(func() { registry.SetDomainResolver(nil) })
	p := &plugin{domain: defaultDomain}
	if got := p.effectiveDomain(); got != "toloka.example" {
		t.Errorf("effectiveDomain = %q, want the admin-configured active domain", got)
	}
	// A test-injected domain still wins — that is what keeps every
	// httptest-based test in this package working.
	p.domain = "127.0.0.1:9999"
	if got := p.effectiveDomain(); got != "127.0.0.1:9999" {
		t.Errorf("effectiveDomain with a test override = %q", got)
	}
}

// TestFingerprintInput_DoesNotBorrowANeighbourRow pins the reason the date
// and size patterns use [^<]* rather than a lazy .*?: with a lazy gap, a
// value cell that stops matching sends the engine hunting through the rest of
// the block, and it attaches a DIFFERENT row's value to the change token.
func TestFingerprintInput_DoesNotBorrowANeighbourRow(t *testing.T) {
	reworded := strings.ReplaceAll(fixtureTorrentBlock,
		`<span title="Розмір частини: 2&nbsp;MB">&nbsp;22.01&nbsp;GB&nbsp;</span>`,
		`невідомо`)
	block, ok := torrentBlock([]byte(reworded))
	if !ok {
		t.Fatal("fixture block not found")
	}
	got := fingerprintInput(block)
	if strings.Contains(got, "size=") {
		t.Errorf("a size was borrowed from another row: %q", got)
	}
	if !strings.Contains(got, "registered=2026-09-03 12:23") {
		t.Errorf("the date field should survive independently: %q", got)
	}
}

func TestSearch_RowClassMayCarryOtherNames(t *testing.T) {
	registry.SetDomainResolver(nil)
	page := strings.ReplaceAll(fixtureSearchHTML, `class="prow1"`, `class="tCenter prow1"`)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(page))
	})
	results, err := p.Search(context.Background(), "x", testCreds())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1 — a row class with extra names must still match", len(results))
	}
}

func TestDo_NonOKStatusIsAnError(t *testing.T) {
	registry.SetDomainResolver(nil)
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				// A body on an error status must never be parsed as a page.
				_, _ = w.Write([]byte(fixtureTopicHTML))
			})
			if _, err := p.fetch(context.Background(), "https://toloka.to/t1", testCreds()); err == nil {
				t.Errorf("status %d must be an error", code)
			}
		})
	}
}

func TestGuards_RejectBadInput(t *testing.T) {
	registry.SetDomainResolver(nil)
	p := &plugin{sessions: forumcommon.New(), domain: defaultDomain}

	if _, err := p.Parse(context.Background(), "https://toloka.to/forum/index.php"); err == nil {
		t.Error("Parse must reject a non-topic URL")
	}
	if err := p.Login(context.Background(), nil); err == nil {
		t.Error("Login must reject nil credentials")
	}
	if err := p.Login(context.Background(), &domain.TrackerCredential{}); err == nil {
		t.Error("Login must reject an empty username")
	}
	// Without credentials gateError cannot consult a session, so it must
	// return the caller's error rather than claim the session expired.
	base := errors.New("boom")
	if got := p.gateError(nil, base); !errors.Is(got, base) {
		t.Errorf("gateError with nil creds = %v, want the original error", got)
	}
}

func TestSearch_CapsAtMaxResults(t *testing.T) {
	registry.SetDomainResolver(nil)
	var rows strings.Builder
	for i := 0; i < maxSearchResults+15; i++ {
		fmt.Fprintf(&rows,
			`<tr class="prow1"><td class="topictitle genmed"><a href="t%d"><b>Release %d</b></td>`+
				`<td class="gensmall">1 GB</td><td class="seedmed"><b>1</b></td></tr>`, 100000+i, i)
	}
	page := "<html><body><table>" + rows.String() + "</table></body></html>"
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(page))
	})
	results, err := p.Search(context.Background(), "many", testCreds())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != maxSearchResults {
		t.Errorf("results = %d, want capped at %d", len(results), maxSearchResults)
	}
}

// TestSearch_UnknownSeedersIsMinusOne pins the registry.SearchResult
// contract: -1 means "unknown", which the UI renders as a dash. Zero would
// claim the release has no seeders at all.
func TestSearch_UnknownSeedersIsMinusOne(t *testing.T) {
	registry.SetDomainResolver(nil)
	page := strings.ReplaceAll(fixtureSearchHTML, `class="seedmed"><b>3</b>`, `class="seedmed">?`)
	p, _ := newTestPlugin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: userCookie, Path: "/"})
		_, _ = w.Write([]byte(page))
	})
	results, err := p.Search(context.Background(), "x", testCreds())
	if err != nil || len(results) != 1 {
		t.Fatalf("Search: err=%v results=%d", err, len(results))
	}
	if results[0].Seeders != -1 {
		t.Errorf("Seeders = %d, want -1 (unknown)", results[0].Seeders)
	}
}

// TestOGImage covers the poster source. Searching the post body for an <img>
// finds only site chrome, avatars and smilies — which is how this was first
// missed and written off as "Toloka has no posters". The poster is in the
// <head>, served through thumb.hurtom.com.
func TestOGImage(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"real captured head", fixtureTopicHTML, "https://thumb.hurtom.com/image/w250/toloka.to/photos/120227013255132137_f0_0.jpg"},
		{"reversed attribute order", `<meta content="https://thumb.hurtom.com/x.jpg" property="og:image">`, "https://thumb.hurtom.com/x.jpg"},
		{"protocol-relative", `<meta property="og:image" content="//thumb.hurtom.com/x.jpg">`, "https://thumb.hurtom.com/x.jpg"},
		{"entities decoded", `<meta property="og:image" content="https://h/x.jpg?a=1&amp;b=2">`, "https://h/x.jpg?a=1&b=2"},
		{"no tag at all", `<html><head></head><body></body></html>`, ""},
		// Never hand the browser something that is not a web URL.
		{"non-web scheme dropped", `<meta property="og:image" content="javascript:alert(1)">`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ogImage([]byte(tt.body)); got != tt.want {
				t.Errorf("ogImage = %q, want %q", got, tt.want)
			}
		})
	}
}
