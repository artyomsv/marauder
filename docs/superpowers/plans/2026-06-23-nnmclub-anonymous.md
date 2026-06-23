# NNM-Club Phase 1 (anonymous) Implementation Plan

> **Status (2026-06-23):** Phase 1 implemented (anonymous-only). **Phase 2 (authenticated) was abandoned** — Cloudflare Turnstile blocks every automated login path; see the design doc's "Phase 2 — ABANDONED" section. The plugin does not implement `WithCredentials`; the add-account UI shows a disclaimer.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing `nnmclub` tracker plugin correctly extract the infohash, return a usable (display-name-enriched) magnet, and resolve title+poster — for everything an unauthenticated user can reach — validated against a real captured page fixture and a live smoke check.

**Architecture:** Surgical fixes to the existing plugin (`backend/internal/plugins/trackers/nnmclub/`). Replace the broken `Info-Hash`-label hash regex with quote-agnostic magnet extraction, rewrite `Download` to return an enriched magnet (no anonymous `download.php` call — it 302s to login), add `WithMetadata.ResolveMetadata`, and replace the fabricated test fixture with one captured from the real page. Authenticated `.torrent` flow is Phase 2 (out of scope).

**Tech Stack:** Go 1.25, stdlib `net/http` + `regexp`, `forumcommon` session/text helpers, `e2etest.RunFullPipeline` harness.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-06-23-nnmclub-anonymous-design.md`.
- **Build/test only in Docker** (never install Go locally). Shorthand used below:
  `DOCKER_GO = docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25`
- **No commits during implementation.** The user commits everything at the end, on `main`. Each task ends with a test-checkpoint, NOT a commit. The final commit (Task 6) is gated on explicit user confirmation.
- **Stay on `main`** — no feature branch.
- **No fabricated data.** The test fixture uses only values observed from the live page `https://nnmclub.to/forum/viewtopic.php?t=420880`:
  - title: `Через тернии к звёздам (1980) DVDRip [H.264] Оригинальная версия :: NNM-Club`
  - magnet: `magnet:?xt=urn:btih:094EC3052ED759240E4DFD89F3F7CA5C5B428FF4` (hash lowercased: `094ec3052ed759240e4dfd89f3f7ca5c5b428ff4`)
  - download link: `download.php?id=379398`
  - og:image: `https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg`
- **Go conventions:** tabs, `gofmt`, wrap errors with `%w`, `MixedCaps`.
- **Confirmed external APIs** (verified to exist — do not re-derive):
  - `forumcommon.CleanTitle(raw, siteSuffix string) string`
  - `forumcommon.DecodeWindows1251(s string) string`
  - `registry.Metadata{ Title string; ImageURL string }`
  - `registry.WithMetadata` requires `ResolveMetadata(ctx, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error)`
  - `e2etest.Case` fields: `ExpectedHash string`, `ExpectTorrentFile bool` (false ⇒ a magnet payload is accepted and asserted through qbitfake).

---

## File Structure

- `backend/internal/plugins/trackers/nnmclub/nnmclub.go` — modify regexes, `Check`, `Download`; add `ResolveMetadata` + `WithMetadata` assertion.
- `backend/internal/plugins/trackers/nnmclub/nnmclub_test.go` — replace fabricated fixture with real-derived HTML; rewrite `TestCheck`/`TestDownload`; add `TestResolveMetadata`.
- `backend/internal/plugins/trackers/nnmclub/nnmclub_e2e_test.go` — serve the real fixture, assert magnet pipeline (`ExpectedHash`, magnet payload).
- `CLAUDE.md` — add `nnmclub` to the `WithMetadata` tracker list.

---

## Task 1: Replace fabricated fixture and fix `Check` hash extraction

**Files:**
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub_test.go:14-19` (fixture const), `:96-109` (TestCheck)
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub.go:139-161` (regexes + Check)

**Interfaces:**
- Consumes: `forumcommon.CleanTitle`, existing `p.fetch`, `titleRe`.
- Produces: package-level `magnetRe = regexp.MustCompile(\`magnet:\?xt=urn:btih:([A-Fa-f0-9]{40})\`)` reused by Task 2; `Check` returning `domain.Check{Hash, DisplayName}`.

- [ ] **Step 1: Replace the fabricated fixture with real-derived HTML**

In `nnmclub_test.go`, replace the `fixtureViewtopicHTML` const (lines 14-19) with:

```go
const fixtureViewtopicHTML = `<html><head>
<title>Через тернии к звёздам (1980) DVDRip [H.264] Оригинальная версия :: NNM-Club</title>
<meta property="og:image" content="https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg"/>
</head>
<body>
<a href="logout.php">logout</a>
<a rel="nofollow" href="magnet:?xt=urn:btih:094EC3052ED759240E4DFD89F3F7CA5C5B428FF4" title="Примагнититься"><img src="https://nnmstatic.win/forum/images/magnet.png"></a>
<a href="download.php?id=379398" rel="nofollow">Скачать</a>
</body></html>`
```

- [ ] **Step 2: Rewrite `TestCheck` to assert the real hash + title**

Replace `TestCheck` (lines 96-109) with:

```go
func TestCheck(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=42"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Hash != "094ec3052ed759240e4dfd89f3f7ca5c5b428ff4" {
		t.Errorf("hash: %q", check.Hash)
	}
	if !strings.Contains(check.DisplayName, "Через тернии к звёздам") {
		t.Errorf("display name: %q", check.DisplayName)
	}
	if strings.HasSuffix(check.DisplayName, " :: NNM-Club") {
		t.Errorf("site suffix not stripped: %q", check.DisplayName)
	}
}
```

- [ ] **Step 3: Run the test to verify it FAILS**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestCheck -v"`
Expected: FAIL — current `hashRe` looks for an `Info-Hash` label absent from the fixture, so `Check` returns `nnm-club: no infohash found`.

- [ ] **Step 4: Fix the regexes and `Check` in `nnmclub.go`**

Replace the `var ( … )` regex block (lines 139-143) with:

```go
var (
	titleRe  = regexp.MustCompile(`(?s)<title>([^<]+)</title>`)
	magnetRe = regexp.MustCompile(`magnet:\?xt=urn:btih:([A-Fa-f0-9]{40})`)
)
```

Replace `Check` (lines 145-161) with:

```go
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = forumcommon.CleanTitle(string(m[1]), " :: NNM-Club")
	}
	if m := magnetRe.FindSubmatch(body); m != nil {
		check.Hash = strings.ToLower(string(m[1]))
	} else {
		return nil, errors.New("nnm-club: no infohash found")
	}
	return check, nil
}
```

- [ ] **Step 5: Run the test to verify it PASSES**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestCheck -v"`
Expected: PASS.

---

## Task 2: Rewrite `Download` to return an enriched magnet

**Files:**
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub.go:163-178` (Download), import block (drop unused `download.php` regex; ensure `net/url` still imported)
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub_test.go:111-121` (TestDownload)

**Interfaces:**
- Consumes: `magnetRe`, `titleRe`, `forumcommon.CleanTitle`, `p.fetch`, `net/url.QueryEscape`.
- Produces: `Download` returning `domain.Payload{MagnetURI: "magnet:?xt=urn:btih:<hash>&dn=<title>"}` with empty `TorrentFile` when no creds.

- [ ] **Step 1: Rewrite `TestDownload` to assert a magnet payload**

Replace `TestDownload` (lines 111-121) with:

```go
func TestDownload(t *testing.T) {
	p := newTestPlugin(t)
	topic := &domain.Topic{URL: "https://" + p.domain + "/forum/viewtopic.php?t=42"}
	payload, err := p.Download(context.Background(), topic, nil, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(payload.TorrentFile) != 0 {
		t.Errorf("expected no torrent file in anonymous mode, got %d bytes", len(payload.TorrentFile))
	}
	if !strings.Contains(payload.MagnetURI, "urn:btih:094ec3052ed759240e4dfd89f3f7ca5c5b428ff4") {
		t.Errorf("magnet missing infohash: %q", payload.MagnetURI)
	}
	if !strings.Contains(payload.MagnetURI, "dn=") {
		t.Errorf("magnet missing display name: %q", payload.MagnetURI)
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestDownload -v"`
Expected: FAIL — current `Download` looks for `download.php` and returns a `TorrentFile`, so `MagnetURI` is empty and `TorrentFile` is non-empty.

- [ ] **Step 3: Rewrite `Download` in `nnmclub.go`**

Replace `Download` (lines 163-178) with:

```go
// Download returns a magnet for the topic. Anonymous (no creds) is the only
// supported mode in Phase 1: the .torrent at download.php is login-gated
// (302 -> login), so we return the page's hash-only magnet enriched with a
// real &dn= display name. The magnet carries no trackers (NNM-Club strips
// them), so peer discovery relies on DHT and may stall — the reliable fix is
// the credentialed .torrent (with NNM-Club's passkey'd announce), Phase 2.
func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	body, err := p.fetch(ctx, topic.URL, creds)
	if err != nil {
		return nil, err
	}
	m := magnetRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("nnm-club: topic page has no magnet link")
	}
	magnet := "magnet:?xt=urn:btih:" + strings.ToLower(string(m[1]))
	if mt := titleRe.FindSubmatch(body); mt != nil {
		if name := forumcommon.CleanTitle(string(mt[1]), " :: NNM-Club"); name != "" {
			magnet += "&dn=" + url.QueryEscape(name)
		}
	}
	return &domain.Payload{MagnetURI: magnet}, nil
}
```

- [ ] **Step 4: Remove the now-unused `download.php` regex if present**

If a `dlHrefRe` package var remains in `nnmclub.go` (it was used only by the old `Download`), delete its declaration. Confirm `net/url` is still in the import block (it is used by `Login` and now `Download`).

- [ ] **Step 5: Run the test to verify it PASSES**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestDownload -v"`
Expected: PASS.

---

## Task 3: Add `WithMetadata.ResolveMetadata`

**Files:**
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub.go` (add `ogImageRe`, `ResolveMetadata`, `WithMetadata` assertion)
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub_test.go` (add `TestResolveMetadata`)

**Interfaces:**
- Consumes: `titleRe`, `forumcommon.CleanTitle`, `p.fetch`, `registry.Metadata`.
- Produces: `ResolveMetadata(ctx, rawURL, creds) (*registry.Metadata, error)` returning `{Title, ImageURL}`.

- [ ] **Step 1: Add `TestResolveMetadata`**

Append to `nnmclub_test.go`:

```go
func TestResolveMetadata(t *testing.T) {
	p := newTestPlugin(t)
	meta, err := p.ResolveMetadata(context.Background(), "https://"+p.domain+"/forum/viewtopic.php?t=42", nil)
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if !strings.Contains(meta.Title, "Через тернии к звёздам") {
		t.Errorf("title: %q", meta.Title)
	}
	if meta.ImageURL != "https://a.radikal.ru/a11/2008/6f/f91ffdbf65b2.jpg" {
		t.Errorf("image url: %q", meta.ImageURL)
	}
}
```

- [ ] **Step 2: Run the test to verify it FAILS**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestResolveMetadata -v"`
Expected: FAIL to compile — `p.ResolveMetadata` undefined.

- [ ] **Step 3: Implement `ResolveMetadata` in `nnmclub.go`**

Add the og:image regex next to the other regexes:

```go
var ogImageRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)
```

Add the capability assertion and method (place after `Download`):

```go
// ResolveMetadata implements registry.WithMetadata: it fetches the topic page
// anonymously and extracts a human title + poster (og:image) for the AddTopic
// preview card.
var _ registry.WithMetadata = (*plugin)(nil)

func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	body, err := p.fetch(ctx, rawURL, creds)
	if err != nil {
		return nil, fmt.Errorf("nnm-club resolve metadata: %w", err)
	}
	meta := &registry.Metadata{}
	if m := titleRe.FindSubmatch(body); m != nil {
		meta.Title = forumcommon.CleanTitle(string(m[1]), " :: NNM-Club")
	}
	if m := ogImageRe.FindSubmatch(body); m != nil {
		meta.ImageURL = strings.TrimSpace(string(m[1]))
	}
	return meta, nil
}
```

- [ ] **Step 4: Run the test to verify it PASSES**

Run: `DOCKER_GO sh -c "go test ./internal/plugins/trackers/nnmclub/ -run TestResolveMetadata -v"`
Expected: PASS.

- [ ] **Step 5: Run the whole package to confirm no regressions**

Run: `DOCKER_GO sh -c "go test -race ./internal/plugins/trackers/nnmclub/..."`
Expected: PASS (TestCanParse, TestUsesCloudflare, TestParse, TestCheck, TestDownload, TestResolveMetadata).

---

## Task 4: e2e magnet pipeline assertion

**Files:**
- Modify: `backend/internal/plugins/trackers/nnmclub/nnmclub_e2e_test.go`

**Interfaces:**
- Consumes: `e2etest.RunFullPipeline`, `e2etest.Case{ExpectedHash, ExpectTorrentFile}`, `e2etest.HostRewriteTransport`.
- Produces: a passing end-to-end test asserting the magnet (hash `094ec3…`) flows Parse→Check→Download→qBit.

- [ ] **Step 1: Rewrite the e2e test to serve the real fixture and assert the magnet**

Replace the body of `nnmclub_e2e_test.go` with (keep the existing package clause and imports; add any missing):

```go
package nnmclub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "nnmclub/anonymous-magnet-then-qbit",
		Setup: func(t *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/forum/viewtopic.php"):
					w.WriteHeader(200)
					_, _ = w.Write([]byte(fixtureViewtopicHTML))
				case r.URL.Path == "/forum/index.php":
					w.WriteHeader(200)
					_, _ = w.Write([]byte(`<a href="logout.php">logout</a>`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(srv.Close)

			testHost := strings.TrimPrefix(srv.URL, "http://")
			p := &plugin{
				sessions: forumcommon.New(),
				domain:   "nnmclub.to",
				transport: &e2etest.HostRewriteTransport{
					From: "nnmclub.to",
					To:   testHost,
				},
			}
			return p, "https://nnmclub.to/forum/viewtopic.php?t=420880"
		},
		// Anonymous Phase 1: no creds, magnet payload expected.
		ExpectedHash:      "094ec3052ed759240e4dfd89f3f7ca5c5b428ff4",
		ExpectTorrentFile: false,
	})
}
```

Note: `fixtureViewtopicHTML` is shared from `nnmclub_test.go` (same package). If a duplicate `e2eTopicHTML` const exists in the e2e file, remove it.

- [ ] **Step 2: Run the e2e test**

Run: `DOCKER_GO sh -c "go test -race ./internal/plugins/trackers/nnmclub/ -run TestE2E -v"`
Expected: PASS. If it fails because `RunFullPipeline` attempts a login that the fixture server 404s, add a `case strings.HasPrefix(r.URL.Path, "/forum/login.php")` arm that writes `<a href="logout.php">logout</a>` (mirroring `nnmclub_test.go`). Re-run; expect PASS.

---

## Task 5: Docs + full backend verification + live smoke (Definition of Done)

**Files:**
- Modify: `CLAUDE.md` (WithMetadata tracker list)

- [ ] **Step 1: Update `CLAUDE.md` WithMetadata list**

Find the `WithMetadata` description line (it reads `WithMetadata (RuTracker, LostFilm, Kinozal) resolves a real title + poster…`) and add NNM-Club:

```
WithMetadata (RuTracker, LostFilm, Kinozal, NNM-Club) resolves a real title + poster image
```

- [ ] **Step 2: Full backend build + vet + test**

Run: `DOCKER_GO sh -c "go build ./... && go vet ./... && go test -race ./..."`
Expected: PASS across the whole backend (catches import/compile regressions and the registry wiring).

- [ ] **Step 3: Live smoke check against the real site**

Bring up the dev stack (per CLAUDE.md) with cfsolver available, then add the example topic and confirm a real infohash comes back. Using the running gateway at `http://localhost:34080` (admin/`pleasechangeme`), via the UI or API:

- Add a topic with URL `https://nnmclub.to/forum/viewtopic.php?t=420880`.
- Confirm `Check` returns hash `094ec3052ed759240e4dfd89f3f7ca5c5b428ff4` and the preview shows the real title + poster.
- Confirm `Download` yields a magnet containing that infohash and a `dn=` name.

Expected: real infohash returned (NOT `nnm-club: no infohash found`). If a plain Go GET is Cloudflare-challenged (non-200 from `p.fetch`), that is the signal to verify the cfsolver route is wired and reachable; capture the failure mode and address before declaring done.

- [ ] **Step 4: Report results to the user**

Summarize: unit + e2e green (paste the `go test` summary line), CLAUDE.md updated, and the live-smoke outcome (real hash + whether Cloudflare needed cfsolver). Do not claim done until Step 3 returns a real hash.

---

## Task 6: Final commit (GATED — only on explicit user confirmation)

**Do not run this task until the user explicitly says to commit.** Per project rules: no AI attribution in the message, imperative mood, ≤72-char subject.

- [ ] **Step 1: Show the diff and ask**

Run: `git status` and `git --no-pager diff --stat`, then ask the user to confirm committing on `main`.

- [ ] **Step 2: Commit (after the user confirms)**

```bash
git add backend/internal/plugins/trackers/nnmclub/ CLAUDE.md docs/superpowers/specs/2026-06-23-nnmclub-anonymous-design.md docs/superpowers/plans/2026-06-23-nnmclub-anonymous.md
git commit -m "feat(nnmclub): fix anonymous infohash + magnet, add metadata"
```

(`feat(nnmclub):` so auto-release cuts a minor bump when this eventually merges. Subject is 53 chars.)

---

## Self-Review

**Spec coverage:**
- Check hash fix → Task 1 ✅
- Download enriched magnet → Task 2 ✅
- WithMetadata title+poster → Task 3 ✅
- Honest real-page fixture → Task 1 Step 1 ✅
- e2e magnet pipeline → Task 4 ✅
- Cloudflare + live smoke DoD → Task 5 Step 3 ✅
- CLAUDE.md WithMetadata list → Task 5 Step 1 ✅
- Phase 2 (login/.torrent/passkey) explicitly deferred → not in plan ✅

**Placeholder scan:** No TBD/TODO; every code step shows complete code. ✅

**Type consistency:** `magnetRe` (Task 1) reused in Task 2; `fixtureViewtopicHTML` (Task 1) reused in Task 4; `ResolveMetadata` signature matches `registry.WithMetadata`; `registry.Metadata{Title, ImageURL}` field names match the verified struct. ✅

**Known limitation (documented, not a gap):** the unit fixture is UTF-8, so it exercises the `CleanTitle` pass-through path but not real cp1251 decoding; the live smoke (Task 5 Step 3) covers the cp1251 path against the real site.
