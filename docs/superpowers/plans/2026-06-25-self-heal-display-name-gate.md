# Placeholder-only Self-Heal Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the scheduler from overwriting a resolved topic title with a noisier `Check` title, by gating display-name self-heal to placeholder-only via a persisted provenance flag — and make Kinozal's `Check` title source consistent with the rest of the plugin.

**Architecture:** A new `topics.display_name_is_placeholder` boolean records whether the stored title is a generated placeholder (`true`) or a resolved/locked title (`false`). Set authoritatively at creation, cleared on first self-heal and on user rename. The scheduler self-heal only fires while the flag is `true`. Separately, Kinozal's `Check` rebuilds its title URL from the trusted `p.domain` (like every other call in the plugin) instead of the raw mirror host.

**Tech Stack:** Go 1.25, pgx v5, goose migrations (`//go:embed`), zerolog. Backend tests run in the `golang:1.25` Docker image (Go is never installed on the host).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-06-25-self-heal-display-name-gate-design.md`.
- Worktree backend path (used in every test command below):
  `E:/Projects/Stukans/Marauder/.worktrees/90-sonarr-imported-topics-show-forum-main-page-title-instead-of-release-name-name-disappears-after-first-check/backend`
- Run any Go command via Docker. Shorthand used in steps:
  ```bash
  WT=E:/Projects/Stukans/Marauder/.worktrees/90-sonarr-imported-topics-show-forum-main-page-title-instead-of-release-name-name-disappears-after-first-check
  dgo() { docker run --rm -v "$WT/backend:/backend" -w //backend golang:1.25 sh -c "$1"; }
  ```
- New migration is `0010` (next after `0009_add_sonarr_settings.sql`), goose format (`-- +goose Up/Down`, `StatementBegin/End`).
- Existing topic rows must default to `false` (lock all existing) — non-negotiable.
- Go conventions: tabs, wrap errors with `%w`, `MixedCaps`, getters without `Get`. Commit messages: imperative ≤72 chars, reference `#90`, **no** AI/authorship trailers.
- Column ordering: append `display_name_is_placeholder` to the **end** of `topicColumns`, the **end** of the `scanTopic` arg list, and as the final column/value in the `Create` INSERT — keep all three aligned.

---

### Task 1: Migration + domain field + repo read/write plumbing

Adds the column and threads it through the domain struct and the `Topics` repo so it round-trips. No behavior change yet (default `false`).

**Files:**
- Create: `backend/internal/db/migrations/0010_topic_display_name_placeholder.sql`
- Modify: `backend/internal/domain/domain.go` (Topic struct, after `ImageURL`)
- Modify: `backend/internal/db/repo/topics.go` (`topicColumns`, `scanTopic`, `Create`)

**Interfaces:**
- Produces: `domain.Topic.DisplayNameIsPlaceholder bool` — read by Task 3 (scheduler gate) and written by Task 2 (creation). Persisted/loaded by the `Topics` repo.

- [ ] **Step 1: Write the migration**

Create `backend/internal/db/migrations/0010_topic_display_name_placeholder.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE topics
    ADD COLUMN display_name_is_placeholder BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE topics
    DROP COLUMN display_name_is_placeholder;
-- +goose StatementEnd
```

- [ ] **Step 2: Add the domain field**

In `backend/internal/domain/domain.go`, add the field immediately after `ImageURL` (line 73):

```go
	DisplayName       string
	ImageURL          string
	// DisplayNameIsPlaceholder is true while DisplayName is a tracker-generated
	// placeholder (e.g. "Kinozal topic 123") eligible for scheduler self-heal.
	// Set false once a real title is resolved (metadata, first self-heal, or a
	// user rename) so self-heal can never downgrade a good title. See issue #90.
	DisplayNameIsPlaceholder bool
```

- [ ] **Step 3: Thread the column through the repo**

In `backend/internal/db/repo/topics.go`:

Append the column to `topicColumns` (currently ends `created_at, updated_at`):

```go
const topicColumns = `id, user_id, tracker_name, url, display_name,
		COALESCE(image_url,''), client_id, notifier_id,
		COALESCE(download_dir,''), COALESCE(category,''), extra, COALESCE(last_hash,''),
		last_checked_at, last_updated_at, next_check_at,
		check_interval_sec, consecutive_errors, status,
		COALESCE(last_error,''), created_at, updated_at, display_name_is_placeholder`
```

Add the scan target as the final argument in `scanTopic` (after `&t.UpdatedAt`):

```go
	err := row.Scan(
		&t.ID, &t.UserID, &t.TrackerName, &t.URL, &t.DisplayName,
		&t.ImageURL, &clientID, &notifierID, &t.DownloadDir, &t.Category, &extraRaw, &t.LastHash,
		&lastChecked, &lastUpdated, &t.NextCheckAt,
		&t.CheckIntervalSec, &t.ConsecutiveErrors, &status,
		&t.LastError, &t.CreatedAt, &t.UpdatedAt, &t.DisplayNameIsPlaceholder,
	)
```

Add the column + value to `Create`'s INSERT (new `$14`):

```go
	q := `
INSERT INTO topics (user_id, tracker_name, url, display_name, image_url, client_id, notifier_id,
                    download_dir, category, extra, check_interval_sec, next_check_at, status,
                    display_name_is_placeholder)
VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14)
RETURNING ` + topicColumns
	row := r.pool.QueryRow(ctx, q,
		t.UserID, t.TrackerName, t.URL, t.DisplayName, t.ImageURL, t.ClientID, t.NotifierID,
		t.DownloadDir, t.Category, extra, t.CheckIntervalSec, t.NextCheckAt, string(t.Status),
		t.DisplayNameIsPlaceholder,
	)
```

- [ ] **Step 4: Verify it builds and the suite is still green**

Run:
```bash
dgo "go build ./... && go vet ./... && go test ./..."
```
Expected: PASS. No behavior change yet; existing tests unaffected (new field defaults to its zero value `false`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/migrations/0010_topic_display_name_placeholder.sql \
        backend/internal/domain/domain.go backend/internal/db/repo/topics.go
git commit -m "feat(#90): add topics.display_name_is_placeholder column"
```

---

### Task 2: Compute the flag at creation (`topics/create.go`)

Set `DisplayNameIsPlaceholder` from the title's source: `true` only when the name fell back to `Parse`'s generated placeholder; `false` for caller-supplied names and resolved metadata titles.

**Files:**
- Modify: `backend/internal/topics/create.go` (`BuildAndCreate`, `resolveMetadata`)
- Test: `backend/internal/topics/create_test.go`

**Interfaces:**
- Consumes: `domain.Topic.DisplayNameIsPlaceholder` (Task 1).
- Produces: topics persisted with the flag set correctly — relied on by Task 3's gate at runtime.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/topics/create_test.go`. First, a metadata-capable fake tracker (place near `fakeTracker`):

```go
// metaTracker is a tracker that also resolves real metadata. Matches
// https://metatopics.test/* and returns a fixed title + image.
type metaTracker struct{}

func (metaTracker) Name() string        { return "metatopics-test" }
func (metaTracker) DisplayName() string { return "Meta Topics Tracker" }
func (metaTracker) CanParse(u string) bool {
	return strings.HasPrefix(u, "https://metatopics.test/")
}
func (metaTracker) Parse(context.Context, string) (*domain.Topic, error) {
	return &domain.Topic{DisplayName: "Meta topic 1", Extra: map[string]any{}}, nil
}
func (metaTracker) Check(context.Context, *domain.Topic, *domain.TrackerCredential) (*domain.Check, error) {
	return nil, nil
}
func (metaTracker) Download(context.Context, *domain.Topic, *domain.Check, *domain.TrackerCredential) (*domain.Payload, error) {
	return nil, nil
}
func (metaTracker) ResolveMetadata(_ context.Context, _ string, _ *domain.TrackerCredential) (*registry.Metadata, error) {
	return &registry.Metadata{Title: "Real Release Name", ImageURL: "https://img.test/p.jpg"}, nil
}

func init() { registry.RegisterTracker(metaTracker{}) }
```

Then the three behavior tests:

```go
func TestBuildAndCreate_PlaceholderName_FlagsPlaceholder(t *testing.T) {
	// fakeTracker has no metadata; name falls back to Parse's "Placeholder".
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{UserID: uuid.New(), URL: goodURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=true for Parse fallback name")
	}
	if store.created.DisplayName != "Placeholder" {
		t.Errorf("display name = %q, want Placeholder", store.created.DisplayName)
	}
}

func TestBuildAndCreate_ResolvedMetadata_FlagsResolved(t *testing.T) {
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: "https://metatopics.test/topic/1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=false when metadata resolved a title")
	}
	if store.created.DisplayName != "Real Release Name" {
		t.Errorf("display name = %q, want Real Release Name", store.created.DisplayName)
	}
}

func TestBuildAndCreate_CallerSuppliedName_FlagsResolved(t *testing.T) {
	store := &fakeStore{}
	_, err := BuildAndCreate(context.Background(), store, CreateInput{
		UserID: uuid.New(), URL: goodURL, DisplayName: "User Chosen",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created.DisplayNameIsPlaceholder {
		t.Errorf("want DisplayNameIsPlaceholder=false for caller-supplied name")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
dgo "go test ./internal/topics/... -run TestBuildAndCreate_PlaceholderName_FlagsPlaceholder -v"
```
Expected: FAIL (`DisplayNameIsPlaceholder` is always false; the resolved/caller tests would pass trivially but the placeholder test fails because nothing sets it `true`).

- [ ] **Step 3: Implement the flag computation**

In `backend/internal/topics/create.go`, change the display-name block in `BuildAndCreate` (currently lines 94–103):

```go
	displayName := in.DisplayName
	resolved := in.DisplayName != "" // caller-supplied name is authoritative
	if displayName == "" {
		displayName = parsed.DisplayName
	}

	// Best-effort metadata: a real title + poster straight from the page so
	// a fresh topic isn't a "RuTracker topic 123" placeholder. Fail-open —
	// any error leaves the placeholder and never blocks creation; the
	// scheduler self-heals the title on the first check.
	imageURL := resolveMetadata(ctx, tracker, in, &displayName, &resolved)
```

Set the field when building the topic (in the `t := &domain.Topic{...}` literal, after `DisplayName: displayName,`):

```go
		DisplayName:              displayName,
		DisplayNameIsPlaceholder: !resolved,
```

Update `resolveMetadata` to report resolution via a pointer:

```go
// resolveMetadata returns the poster URL and, when the user didn't supply a
// name, upgrades displayName in place and flips *resolved to true. Fail-open:
// errors are swallowed, leaving the placeholder name and *resolved unchanged.
func resolveMetadata(ctx context.Context, tracker registry.Tracker, in CreateInput, displayName *string, resolved *bool) string {
	wm, ok := tracker.(registry.WithMetadata)
	if !ok {
		return ""
	}
	mctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	meta, err := wm.ResolveMetadata(mctx, in.URL, nil)
	if err != nil || meta == nil {
		return ""
	}
	if in.DisplayName == "" && meta.Title != "" {
		*displayName = meta.Title
		*resolved = true
	}
	return meta.ImageURL
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
dgo "go test ./internal/topics/... -v"
```
Expected: PASS (all three new tests + existing `TestBuildAndCreate_*`).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/topics/create.go backend/internal/topics/create_test.go
git commit -m "feat(#90): flag placeholder vs resolved title at topic creation"
```

---

### Task 3: Gate the scheduler self-heal (`scheduler.go`)

Self-heal fires only while the stored name is a placeholder.

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go:404` (gate condition)
- Test: `backend/internal/scheduler/scheduler_test.go` (`fakeTopics` gains `UpdateDisplayName`; two tests)

**Interfaces:**
- Consumes: `domain.Topic.DisplayNameIsPlaceholder` (Task 1); the existing `displayNamePersister` interface (`UpdateDisplayName(ctx, id, name) error`).

- [ ] **Step 1: Make `fakeTopics` a `displayNamePersister` and write the failing tests**

In `backend/internal/scheduler/scheduler_test.go`, add a record type + field + method near `fakeTopics` (after the `UpdateExtra` method, ~line 119):

```go
type updateDisplayNameCall struct {
	id   uuid.UUID
	name string
}
```

Add the field to the `fakeTopics` struct (alongside `updateExtraCalls`):

```go
	updateDisplayNameCalls []updateDisplayNameCall
```

Add the method:

```go
func (f *fakeTopics) UpdateDisplayName(_ context.Context, id uuid.UUID, name string) error {
	f.updateDisplayNameCalls = append(f.updateDisplayNameCalls, updateDisplayNameCall{id, name})
	return nil
}
```

Add the two gate tests (in the Tests section):

```go
func TestRunCheck_SelfHeal_PlaceholderName_Persists(t *testing.T) {
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "old-hash", DisplayName: "Real Title"}, err: nil},
		},
	}
	f := newFixture(t, tr, false)
	f.topic.DisplayName = "Fake topic 1"
	f.topic.DisplayNameIsPlaceholder = true

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.updateDisplayNameCalls) != 1 {
		t.Fatalf("want 1 UpdateDisplayName call, got %d", len(f.topics.updateDisplayNameCalls))
	}
	if got := f.topics.updateDisplayNameCalls[0].name; got != "Real Title" {
		t.Errorf("healed name = %q, want Real Title", got)
	}
}

func TestRunCheck_SelfHeal_ResolvedName_DoesNotDowngrade(t *testing.T) {
	// Regression for #90: a resolved title must NOT be overwritten even when
	// Check reports a different (e.g. main-page) DisplayName.
	tr := &fakeTracker{
		name: "faketracker",
		checks: []checkResult{
			{check: &domain.Check{Hash: "old-hash", DisplayName: "Kinozal.TV Main Page"}, err: nil},
		},
	}
	f := newFixture(t, tr, false)
	f.topic.DisplayName = "Real Release Name"
	f.topic.DisplayNameIsPlaceholder = false

	f.s.runCheck(context.Background(), f.s.log, f.topic)

	if len(f.topics.updateDisplayNameCalls) != 0 {
		t.Errorf("want 0 UpdateDisplayName calls for a resolved title, got %d",
			len(f.topics.updateDisplayNameCalls))
	}
}
```

- [ ] **Step 2: Run tests to verify the regression test fails**

Run:
```bash
dgo "go test ./internal/scheduler/... -run TestRunCheck_SelfHeal -v"
```
Expected: `TestRunCheck_SelfHeal_ResolvedName_DoesNotDowngrade` FAILS (current code calls `UpdateDisplayName` because there's no flag check); `TestRunCheck_SelfHeal_PlaceholderName_Persists` PASSES.

- [ ] **Step 3: Add the gate condition**

In `backend/internal/scheduler/scheduler.go`, change the self-heal condition (line 404):

```go
	// Self-heal the display name only while it's still a generated placeholder.
	// Once a real title is resolved (add-time metadata, a prior self-heal, or a
	// user rename) the topic is locked, so a noisier Check title can't downgrade
	// it (issue #90).
	if check.DisplayName != "" && check.DisplayName != t.DisplayName && t.DisplayNameIsPlaceholder {
		if p, ok := s.topics.(displayNamePersister); ok {
			if err := p.UpdateDisplayName(ctx, t.ID, check.DisplayName); err != nil {
				log.Warn().Err(err).Msg("UpdateDisplayName failed")
			}
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
dgo "go test ./internal/scheduler/... -v"
```
Expected: PASS (both new tests + the full existing scheduler suite — existing tests use checks with empty `DisplayName`, so the gate is irrelevant to them).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scheduler/scheduler.go backend/internal/scheduler/scheduler_test.go
git commit -m "fix(#90): gate display-name self-heal to placeholder titles only"
```

---

### Task 4: Lock the flag in the repo write paths (`repo/topics.go`)

`UpdateDisplayName` clears the flag atomically with the name; user `Update` clears it only on an actual name change.

**Files:**
- Modify: `backend/internal/db/repo/topics.go` (`UpdateDisplayName`, `Update`)

**Interfaces:**
- Consumes: the `display_name_is_placeholder` column (Task 1).
- Note: the SQL change is verified by build + the full suite (the repo's SQL is exercised by integration, not pgxmock unit tests; behavior at the Go layer is already covered by Tasks 2–3 and the handler tests).

- [ ] **Step 1: Clear the flag in `UpdateDisplayName`**

In `backend/internal/db/repo/topics.go`, update the SQL in `UpdateDisplayName` (currently line 290) so resolving the name also locks it:

```go
	_, err := r.pool.Exec(ctx,
		`UPDATE topics SET display_name = $2, display_name_is_placeholder = false, updated_at = now() WHERE id = $1`,
		id, displayName,
	)
```

- [ ] **Step 2: Clear the flag on a genuine rename in `Update`**

In `Update` (currently lines 267–271), add the `CASE` so editing other fields on a still-placeholder topic doesn't freeze the placeholder:

```go
	row := r.pool.QueryRow(ctx, `UPDATE topics SET
		display_name = $3, client_id = $4, notifier_id = $5, download_dir = $6, category = $7,
		extra = $8,
		display_name_is_placeholder = CASE WHEN display_name <> $3 THEN false ELSE display_name_is_placeholder END,
		updated_at = now()
	WHERE id = $1 AND user_id = $2
	RETURNING `+topicColumns, id, userID, displayName, clientID, notifierID, downloadDir, category, raw)
```

(The `CASE` reads the pre-`SET` `display_name` value, which is correct in Postgres — column references in `SET` expressions see the row's old values.)

- [ ] **Step 3: Verify build + full suite**

Run:
```bash
dgo "go build ./... && go vet ./... && go test ./..."
```
Expected: PASS. Existing handler tests (`TestTopicsUpdate_*`) use a fake store and remain green; this changes only the real SQL.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/db/repo/topics.go
git commit -m "feat(#90): lock display-name flag on resolve and user rename"
```

---

### Task 5: Canonicalize Kinozal's `Check` title host (`kinozal.go`)

`Check` builds its title URL from the trusted `p.domain` instead of the raw mirror host, matching `ResolveMetadata`/`fetchInfohash`/`Download`.

**Files:**
- Modify: `backend/internal/plugins/trackers/kinozal/kinozal.go` (add `canonicalDetailsURL`; use it in `Check` and `ResolveMetadata`)
- Test: `backend/internal/plugins/trackers/kinozal/kinozal_test.go`

**Interfaces:**
- Produces: `func (p *plugin) canonicalDetailsURL(rawURL string) (string, error)` — returns `https://<p.domain>/details.php?id=<n>`.

- [ ] **Step 1: Write the failing tests**

Add to `backend/internal/plugins/trackers/kinozal/kinozal_test.go`:

```go
func TestCanonicalDetailsURL_RebuildsFromDomain(t *testing.T) {
	p := &plugin{domain: "kinozal.tv"}
	got, err := p.canonicalDetailsURL("https://kinozal.guru/details.php?id=2072973")
	if err != nil {
		t.Fatalf("canonicalDetailsURL: %v", err)
	}
	if want := "https://kinozal.tv/details.php?id=2072973"; got != want {
		t.Errorf("canonicalDetailsURL = %q, want %q", got, want)
	}
}

// hostRecordingRewrite records the Host of every outgoing request, then
// redirects it to the test server (target) over http so no real network is hit.
type hostRecordingRewrite struct {
	target string // test server host:port (p.domain)
	hosts  []string
}

func (h *hostRecordingRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	h.hosts = append(h.hosts, req.URL.Host)
	req.URL.Scheme = "http"
	req.URL.Host = h.target
	return http.DefaultTransport.RoundTrip(req)
}

func TestCheck_CanonicalizesMirrorHost(t *testing.T) {
	p := newTestPlugin(t)            // p.domain == test server host
	rec := &hostRecordingRewrite{target: p.domain}
	p.transport = rec               // applied per-session inside fetch()

	// Topic URL points at a different mirror than p.domain.
	topic := &domain.Topic{URL: "https://kinozal.guru/details.php?id=99999"}
	check, err := p.Check(context.Background(), topic, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(check.DisplayName, "The Movie") {
		t.Errorf("display name = %q, want it to contain The Movie", check.DisplayName)
	}
	if len(rec.hosts) == 0 {
		t.Fatal("no requests recorded")
	}
	// The FIRST request is the title/details fetch — it must target p.domain,
	// not the raw kinozal.guru mirror.
	if rec.hosts[0] != p.domain {
		t.Errorf("title fetch host = %q, want canonical %q", rec.hosts[0], p.domain)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
dgo "go test ./internal/plugins/trackers/kinozal/... -run 'TestCanonicalDetailsURL_RebuildsFromDomain|TestCheck_CanonicalizesMirrorHost' -v"
```
Expected: FAIL — `canonicalDetailsURL` undefined (compile error); once defined but before `Check` uses it, `TestCheck_CanonicalizesMirrorHost` fails with `host = "kinozal.guru"`.

- [ ] **Step 3: Add the helper and use it in `Check` and `ResolveMetadata`**

In `backend/internal/plugins/trackers/kinozal/kinozal.go`, add the helper (place it just above `Check`, after the `var (...)` regex block):

```go
// canonicalDetailsURL rebuilds the details URL from the trusted host (p.domain)
// + the numeric id parsed from rawURL — never the raw user URL. Avoids request
// forgery (CodeQL go/request-forgery) and pins the request to p.domain so
// Check's title matches ResolveMetadata's (issue #90).
func (p *plugin) canonicalDetailsURL(rawURL string) (string, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return "", errors.New("kinozal: not a details URL")
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return "", fmt.Errorf("kinozal: topic id: %w", err)
	}
	return fmt.Sprintf("https://%s/details.php?id=%d", p.domain, id), nil
}
```

Change `Check` (currently line 188–192) to fetch the canonical URL for the title:

```go
func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	canonical, err := p.canonicalDetailsURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return nil, err
	}
```

Simplify `ResolveMetadata` to reuse the helper (replace the parse + `canonical :=` block, currently lines 243–255, down to the `body, err := p.fetch(...)` line):

```go
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	canonical, err := p.canonicalDetailsURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	body, err := p.fetch(ctx, canonical, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
```

(Leave the rest of `ResolveMetadata` — title/image extraction — unchanged. `fetchInfohash` already canonicalizes via `p.domain` and is left untouched.)

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
dgo "go test ./internal/plugins/trackers/kinozal/... -v"
```
Expected: PASS (new tests + existing `TestCheck_ServerDetails_ResolvesHashAndTitle`, `TestResolveMetadata_*`, etc. — they use a topic URL whose host already equals `p.domain`, so canonicalization is a no-op for them).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/trackers/kinozal/kinozal.go \
        backend/internal/plugins/trackers/kinozal/kinozal_test.go
git commit -m "fix(#90): canonicalize kinozal Check title host to p.domain"
```

---

### Task 6: Docs + full verification

Record the new column/behavior in `CLAUDE.md` (per the documentation-maintenance rule) and run the whole backend suite once more.

**Files:**
- Modify: `CLAUDE.md` (the `db`/`db/repo` `Topics` row and the `scheduler` self-heal note)

- [ ] **Step 1: Update `CLAUDE.md`**

In the `db` / `db/repo` table row for `Topics`, append a sentence noting the new column. Add after the existing `GetByURL(...)` mention:

```
`Topics` also carries `display_name_is_placeholder` (migration `0010`): true while
the title is a tracker-generated placeholder, set false once resolved
(metadata, first self-heal, or a user rename). `UpdateDisplayName` clears it;
`Update` clears it only when the submitted name changes.
```

In the `WithMetadata` paragraph (the scheduler self-heal note about `displayNamePersister` → `Topics.UpdateDisplayName`), append:

```
The self-heal is gated to placeholder titles only (`display_name_is_placeholder`)
so a noisier `Check` title can never downgrade an already-resolved name (issue #90).
```

- [ ] **Step 2: Full build, vet, race test**

Run:
```bash
dgo "go build ./... && go vet ./... && go test -race ./..."
```
Expected: PASS across all packages.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(#90): document display-name placeholder flag and gated self-heal"
```

---

## Self-Review

**Spec coverage:**
- § 1 Data model → Task 1 ✓
- § 2 Creation → Task 2 ✓
- § 3 Scheduler gate → Task 3 ✓
- § 4 Repository (UpdateDisplayName clear + Update CASE) → Task 4 ✓
- § 5 Kinozal canonicalization → Task 5 ✓
- Testing matrix → Tasks 2, 3, 5 (unit); Tasks 1, 4, 6 (build/suite); migration applies via goose on boot ✓
- Existing data "lock all existing" → migration `DEFAULT false` (Task 1) ✓
- Docs (files-touched list) → Task 6 ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code; every run step shows command + expected result.

**Type consistency:** `DisplayNameIsPlaceholder bool` used identically in domain (Task 1), create (Task 2), scheduler test (Task 3). `canonicalDetailsURL(string) (string, error)` defined and called consistently (Task 5). `UpdateDisplayName(ctx, id, name) error` matches the existing `displayNamePersister` interface used by the scheduler and the `fakeTopics` method added in Task 3.

**Notes / residual risk:**
- Task 4's SQL `CASE` is not covered by a Go unit test (the repo SQL runs only against a real Postgres). It is low-risk and verified by build + the full suite; the Go-layer behavior it supports is covered by Tasks 2–3 and existing handler tests.
- Already-broken topics remain frozen until a manual rename (spec "Existing data"). No backfill task by design.

## Follow-up (out of scope — separate tickets)
- Honor the actual mirror domain (`.guru`/`.me`) across the whole kinozal plugin instead of pinning `kinozal.tv`.
- Optional per-topic "re-resolve metadata" action to repair already-broken topics on demand.
