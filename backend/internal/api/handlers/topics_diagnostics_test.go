package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/pageredact"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeRawPageTracker is a tracker that owns one URL prefix and can hand back
// a canned page.
type fakeRawPageTracker struct {
	name string
	page string
	err  error
	// hold, when non-nil, blocks ExportRegions until it is closed — for the
	// single-flight test. started is closed once, on the first call: the same
	// test calls the handler again after the gate releases.
	hold      chan struct{}
	started   chan struct{}
	startOnce sync.Once
}

func (f *fakeRawPageTracker) Name() string        { return f.name }
func (f *fakeRawPageTracker) DisplayName() string { return "Fake " + f.name }
func (f *fakeRawPageTracker) CanParse(u string) bool {
	return strings.Contains(u, f.name+".test")
}
func (f *fakeRawPageTracker) Parse(context.Context, string) (*domain.Topic, error) { return nil, nil }
func (f *fakeRawPageTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (f *fakeRawPageTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (f *fakeRawPageTracker) ExportRegions(ctx context.Context, _ string, _ *domain.TrackerCredential) ([]registry.PageRegion, error) {
	if f.started != nil {
		f.startOnce.Do(func() { close(f.started) })
	}
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return []registry.PageRegion{{Name: "page", HTML: []byte(f.page)}}, nil
}

func diagnosticsRequest(t *testing.T, topicID, userID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/topics/"+topicID.String()+"/diagnostics/page", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: userID.String()}))
	return withURLParam(req, "id", topicID.String())
}

func decodeDiagnostics(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body %q)", err, rec.Body.String())
	}
	return got
}

// TestDiagnosticsPage_ReturnsTheRedactedPage is the whole feature in one
// test: the page comes back, and the session id in it does not.
func TestDiagnosticsPage_ReturnsTheRedactedPage(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	const page = `<a href="viewtopic.php?t=1&amp;sid=wCll0mxQk34M71ITmQdA">x</a>` +
		`<th class="seedmed">Some.Release.torrent</th>`
	registry.RegisterTracker(&fakeRawPageTracker{name: "diagraw", page: page})

	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: topicID, UserID: userID, URL: "https://diagraw.test/viewtopic.php?t=1",
	}}
	h := &Topics{Topics: store}
	rec := httptest.NewRecorder()
	h.DiagnosticsPage(rec, diagnosticsRequest(t, topicID, userID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	got := decodeDiagnostics(t, rec)
	html, _ := got["html"].(string)
	if strings.Contains(html, "wCll0mxQk34M71ITmQdA") {
		t.Error("the session id reached the response — this endpoint hands it to a public bug report")
	}
	if !strings.Contains(html, `<th class="seedmed">Some.Release.torrent</th>`) {
		t.Errorf("the markup we collect this page FOR did not survive: %q", html)
	}
	if got["redacted"] != true {
		t.Errorf("redacted = %v, want true", got["redacted"])
	}
	if got["redaction_mark"] != pageredact.Placeholder {
		t.Errorf("redaction_mark = %v, want %q", got["redaction_mark"], pageredact.Placeholder)
	}
	if got["tracker"] != "diagraw" {
		t.Errorf("tracker = %v", got["tracker"])
	}
}

// TestDiagnosticsPage_UnknownTopicIs404 — and a topic somebody else owns is
// the same 404, since GetByID keys on the owner.
func TestDiagnosticsPage_UnknownTopicIs404(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	h := &Topics{Topics: &fakeTopicStore{getByIDErr: repo.ErrNotFound}}
	rec := httptest.NewRecorder()
	h.DiagnosticsPage(rec, diagnosticsRequest(t, topicID, userID))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestDiagnosticsPage_TrackerWithoutTheCapabilityIs409. The frontend hides the
// action, so this is the stale-page case — it must not read as a server fault.
func TestDiagnosticsPage_TrackerWithoutTheCapabilityIs409(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	registry.RegisterTracker(&fakeSearchTracker{name: "diagplain"})
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: topicID, UserID: userID, URL: "https://diagplain.test/t/1",
	}}
	h := &Topics{Topics: store}
	rec := httptest.NewRecorder()
	h.DiagnosticsPage(rec, diagnosticsRequest(t, topicID, userID))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (body %q)", rec.Code, rec.Body.String())
	}
}

// TestDiagnosticsPage_SecondCallWhileRunningIs429. This endpoint fires a live
// authenticated request at a third-party tracker; several rate-limit hard
// enough to answer 429 themselves, so a double-click must not be forwarded.
func TestDiagnosticsPage_SecondCallWhileRunningIs429(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	slow := &fakeRawPageTracker{
		name:    "diagslow",
		page:    "<p>ok</p>",
		hold:    make(chan struct{}),
		started: make(chan struct{}),
	}
	registry.RegisterTracker(slow)
	store := &fakeTopicStore{getByID: &domain.Topic{
		ID: topicID, UserID: userID, URL: "https://diagslow.test/t/1",
	}}
	h := &Topics{Topics: store}

	var wg sync.WaitGroup
	wg.Add(1)
	first := httptest.NewRecorder()
	go func() {
		defer wg.Done()
		h.DiagnosticsPage(first, diagnosticsRequest(t, topicID, userID))
	}()
	<-slow.started

	second := httptest.NewRecorder()
	h.DiagnosticsPage(second, diagnosticsRequest(t, topicID, userID))
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second call status = %d, want 429", second.Code)
	}

	close(slow.hold)
	wg.Wait()
	if first.Code != http.StatusOK {
		t.Errorf("first call status = %d, want 200", first.Code)
	}

	// The gate must release, or one slow fetch locks the topic out forever.
	third := httptest.NewRecorder()
	h.DiagnosticsPage(third, diagnosticsRequest(t, topicID, userID))
	if third.Code != http.StatusOK {
		t.Errorf("after completion status = %d, want 200 — the gate latched shut", third.Code)
	}
}

// fakeWarmRawTracker is a credentialed tracker whose login can be made to
// fail, recording which credential ExportRegions was handed.
type fakeWarmRawTracker struct {
	fakeRawPageTracker
	verifyOK bool
	loginErr error
	gotCreds *domain.TrackerCredential
	called   bool
}

func (f *fakeWarmRawTracker) Login(context.Context, *domain.TrackerCredential) error {
	return f.loginErr
}
func (f *fakeWarmRawTracker) Verify(context.Context, *domain.TrackerCredential) (bool, error) {
	return f.verifyOK, nil
}
func (f *fakeWarmRawTracker) ExportRegions(_ context.Context, _ string, creds *domain.TrackerCredential) ([]registry.PageRegion, error) {
	f.called, f.gotCreds = true, creds
	return []registry.PageRegion{{Name: "page", HTML: []byte(f.page)}}, nil
}

// TestDiagnosticsPage_RedactsTheUsernameEvenWhenLoginFails is F11 from the
// independent review of #193. A failed login must still fall back to an
// anonymous fetch — that part was right — but the account name is still
// known from the stored credential, and a guest page can still show it: the
// reporter's own posts, their uploads. Redaction used to depend on the login
// succeeding, so their identity stayed in the file exactly when something had
// already gone wrong.
func TestDiagnosticsPage_RedactsTheUsernameEvenWhenLoginFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verifyOK  bool
		loginErr  error
		wantAuthd bool
	}{
		{"login fails, fetch goes anonymous", false, errors.New("login failed"), false},
		{"session is warm", true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mk := testMasterKey(t)
			trackerName := "diagwarm" + map[bool]string{true: "ok", false: "fail"}[tc.verifyOK]
			cred := encryptedCred(t, mk, trackerName)
			tr := &fakeWarmRawTracker{
				fakeRawPageTracker: fakeRawPageTracker{name: trackerName, page: `<span class="author">user</span>`},
				verifyOK:           tc.verifyOK,
				loginErr:           tc.loginErr,
			}
			registry.RegisterTracker(tr)
			topicID := uuid.New()
			store := &fakeTopicStore{getByID: &domain.Topic{
				ID: topicID, UserID: cred.UserID, URL: "https://" + trackerName + ".test/t/1",
			}}
			h := &Topics{Topics: store, Creds: &fakeSearchCredStore{cred: cred}, Master: mk}

			rec := httptest.NewRecorder()
			h.DiagnosticsPage(rec, diagnosticsRequest(t, topicID, cred.UserID))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
			}
			got := decodeDiagnostics(t, rec)
			if got["authenticated"] != tc.wantAuthd {
				t.Errorf("authenticated = %v, want %v", got["authenticated"], tc.wantAuthd)
			}
			html, _ := got["html"].(string)
			if want := `<span class="author">` + pageredact.Placeholder + `</span>`; !strings.Contains(html, want) {
				t.Errorf("html = %q, want it to contain %q", html, want)
			}
			if strings.Contains(html, ">user<") {
				t.Errorf("the username survived: %q", html)
			}
			if !tc.wantAuthd && tr.gotCreds != nil {
				t.Error("a failed login must fetch anonymously; ExportRegions got a credential")
			}
		})
	}
}

// TestBuildExport_SaysWhatIsMissing. A region the page did not have is the
// most useful line in a report about a lost session — a guest page has no
// torrent table — so it is named in the file rather than silently dropped.
func TestBuildExport_SaysWhatIsMissing(t *testing.T) {
	doc, meta, err := buildExport(exportHeader{tracker: "tapochek", url: "https://t.test/?t=1"},
		[]registry.PageRegion{
			{Name: "title", HTML: []byte("<title>x</title>")},
			{Name: "torrent-table"},
		}, "")
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	for _, want := range []string{
		"<!-- region: title -->\n<title>x</title>\n",
		"<!-- region: torrent-table: NOT FOUND on this page -->",
		"Everything else on the page was left out on purpose.",
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("export is missing %q:\n%s", want, doc)
		}
	}
	if len(meta) != 2 || !meta[0].Found || meta[1].Found {
		t.Errorf("meta = %+v, want title found and torrent-table not", meta)
	}
}

// TestBuildExport_RedactsTheHeader. The header carries the topic URL exactly
// as the user stored it, and a URL copied from a browser can carry a session.
func TestBuildExport_RedactsTheHeader(t *testing.T) {
	doc, _, err := buildExport(exportHeader{
		tracker: "tapochek", url: "https://t.test/viewtopic.php?t=1&sid=SYNTHETIC_SECRET",
	}, nil, "")
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	if strings.Contains(string(doc), "SYNTHETIC_SECRET") {
		t.Errorf("the session id in the stored URL reached the export:\n%s", doc)
	}
}

// TestCommentSafe_CannotCloseTheHeaderComment. The URL is user-supplied and
// only its prefix is validated, so it must not be able to end the comment it
// is written into — including through a run of three dashes, which a single
// ReplaceAll pass turns into `- --` and leaves closable.
func TestCommentSafe_CannotCloseTheHeaderComment(t *testing.T) {
	for _, in := range []string{
		"https://t.test/?t=1--><script>x</script>",
		"a---b", "a----b", "--!>", "-->",
	} {
		if got := commentSafe(in); strings.Contains(got, "--") {
			t.Errorf("commentSafe(%q) = %q still contains --", in, got)
		}
	}
}
