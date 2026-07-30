# Topic Reset & Re-download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Reset action — per topic and over a multi-select — that discards a topic's check/download state, removes the already-delivered torrents from the client, and queues an immediate re-check, so a topic re-downloads from scratch without losing its configuration.

**Architecture:** Two new repo methods clear the state (`Topics.ResetCheckState`, `Deliveries.DeleteForTopic`). Client removal is extracted out of the scheduler into a new `internal/clientremove` package shared by the scheduler's replace-on-update policy and the new `POST /topics/{id}/reset` handler. "Re-check now" is a pure DB write (`next_check_at = now()`) — the scheduler's due query does the rest, so no scheduler API is added. The frontend adds an inline confirm card (no modal primitive exists in this project) shared by a row button and the bulk action bar.

**Tech Stack:** Go 1.25 (chi, pgx v5, pgxmock v3, zerolog, prometheus), React 19 + TypeScript (React Query, zustand, framer-motion, lucide-react, Tailwind 4), Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-07-30-topic-reset-and-redownload-design.md`

## Global Constraints

- **No AI/agentic attribution** in commit messages — no `Co-Authored-By`, no model or vendor names, no "generated with" notes. Describe what changed and why.
- **Commit messages:** imperative mood, max 72 chars on the first line, body for non-trivial changes.
- **Go:** tabs, `gofmt` mandatory, wrap errors with `fmt.Errorf("...: %w", err)`, `errors.Is` for comparison, consumer-side interfaces (1-2 methods preferred), no `util`/`common` packages.
- **TypeScript:** 2-space indent, `interface` over `type` for object shapes, `@/` path alias, React Query keys from `lib/queryKeys.ts` only, `useT()` for user-facing strings, lucide-react for icons, max 250 lines per component file.
- **Never hand-edit `frontend/src/components/ui/`** — shadcn-managed.
- **No synthetic data.** Test fixtures are pure-function inputs only; nothing fabricated ever reaches a user-visible table or the DB.
- **Backend commands run in Docker** (Go is not installed on the host):
  ```
  docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "<cmd>"
  ```
- **Frontend commands run from the container-local node_modules volume** (the Windows bind mount is too slow for vitest workers):
  ```
  docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
    sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; <cmd>"
  ```
  One-time volume setup if `marauder-fe-nm` does not exist:
  ```
  docker volume create marauder-fe-nm
  docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app \
    node:22-alpine sh -c "cp /host/package.json /host/package-lock.json /app/ && cd /app && npm ci"
  ```
- **Final gate for the whole plan:** `go build ./... && go vet ./... && go test -race ./...` green, and `npx tsc --noEmit && npx vitest run` green.

---

## File Structure

**Create:**

| Path | Responsibility |
|---|---|
| `backend/internal/clientremove/clientremove.go` | Resolve client → plugin → decrypt → `WithRemoval.Remove`, per-client `Result`. No logging, no metrics. |
| `backend/internal/clientremove/clientremove_test.go` | Table-driven coverage of every failure branch. |
| `backend/internal/api/handlers/topics_reset_test.go` | Handler tests for `POST /topics/{id}/reset`. |
| `frontend/src/components/topics/ResetTopicCard.tsx` | Inline confirm + result card, shared by row and bulk reset. |
| `frontend/src/components/topics/ResetTopicCard.test.tsx` | Component tests. |
| `frontend/src/components/topics/TopicRow.tsx` | One topic list row (extracted from `Topics.tsx`). |
| `frontend/src/components/topics/BulkActionBar.tsx` | Multi-select action bar (extracted). |
| `frontend/src/components/topics/DensityToggle.tsx` | Comfortable/compact toggle (extracted). |
| `frontend/src/components/topics/TopicsEmptyState.tsx` | Empty-list state (extracted). |
| `frontend/src/components/topics/StatusIndicator.tsx` | Pulsing status dot (extracted). |

**Modify:**

| Path | Change |
|---|---|
| `backend/internal/db/repo/topics.go` | Add `ResetCheckState`. |
| `backend/internal/db/repo/topics_test.go` | Tests for it. |
| `backend/internal/db/repo/deliveries.go` | Add `DeleteForTopic`. |
| `backend/internal/db/repo/deliveries_test.go` | Tests for it. |
| `backend/internal/scheduler/scheduler.go` | `replacePrevious` uses `clientremove`; delete `removeFromClient`. |
| `backend/internal/events/event.go` | Add `TopicReset` type + policy. |
| `backend/internal/api/handlers/topics.go` | `Reset` handler, seam methods, `Remover` field. |
| `backend/internal/api/handlers/topics_handler_test.go` | `fakeTopicStore` gains `ResetCheckState`. |
| `backend/internal/api/handlers/topics_status_test.go` | `fakeDeliveriesStore` gains `DeleteForTopic`. |
| `backend/internal/metrics/metrics.go` | Add `TopicResetRemovedTotal`. |
| `backend/internal/api/router.go` | Route + `Remover` wiring. |
| `frontend/src/lib/api.ts` | `resetTopic` + `TopicResetResult`. |
| `frontend/src/lib/events.ts` | `topic.reset` label entry. |
| `frontend/src/lib/events-stream.ts` | `topic.reset` invalidation arm in `applyEvent`. |
| `frontend/src/i18n/en.ts`, `ru.ts` | New `topics.reset.*` + `events.topic_reset` keys. |
| `frontend/src/pages/Topics.tsx` | Extract five sub-components out (Task 10), then add reset wiring (Task 11). |
| `CLAUDE.md` | Document `clientremove`, the reset endpoint, the new event. |
| `CHANGELOG.md` | `[Unreleased]` entry. |

---

### Task 1: `Topics.ResetCheckState` repo method

**Files:**
- Modify: `backend/internal/db/repo/topics.go` (add after `MarkEpisodeDownloaded`, which ends at line 258)
- Test: `backend/internal/db/repo/topics_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (r *Topics) ResetCheckState(ctx context.Context, id, userID uuid.UUID) error` — returns `ErrNotFound` when zero rows match.

**Context you need:** these tests use `pgxmock`, not a live Postgres. `mock.ExpectExec(pattern)` matches `pattern` as a **regular expression** against the SQL string, so the assertions below pin the clauses that matter. `assertExpectationsMet` and `newMockTopics` already exist at the top of `topics_test.go` — reuse them, don't redefine.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/db/repo/topics_test.go`:

```go
// ---------- ResetCheckState ----------

func TestTopics_ResetCheckState_ClearsCheckStateAndQueuesRecheck(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	id, userID := uuid.New(), uuid.New()

	// One statement must carry every clause the reset depends on. Pinning
	// them individually means a future edit that silently drops, say, the
	// JSONB key delete fails here instead of in production, where the
	// symptom would be "episodic topics never re-download".
	mock.ExpectExec(`(?s)UPDATE topics SET.*` +
		`last_hash\s+= NULL.*` +
		`consecutive_errors\s+= 0.*` +
		`last_error_code\s+= ''.*` +
		`next_check_at\s+= now\(\).*` +
		`status\s+= CASE WHEN status = 'paused' THEN 'paused' ELSE 'active' END.*` +
		`extra\s+= COALESCE\(extra, '\{\}'::jsonb\) - 'downloaded_episodes'.*` +
		`WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.ResetCheckState(context.Background(), id, userID); err != nil {
		t.Fatalf("ResetCheckState: unexpected error: %v", err)
	}
}

func TestTopics_ResetCheckState_NotFound(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	mock.ExpectExec(`UPDATE topics SET`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.ResetCheckState(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResetCheckState: want ErrNotFound, got %v", err)
	}
}

func TestTopics_ResetCheckState_DBError(t *testing.T) {
	repo, mock := newMockTopics(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	dbErr := errors.New("connection refused")
	mock.ExpectExec(`UPDATE topics SET`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(dbErr)

	err := repo.ResetCheckState(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, dbErr) {
		t.Fatalf("ResetCheckState: want wrapped %v, got %v", dbErr, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test ./internal/db/repo/ -run TestTopics_ResetCheckState -v"
```
Expected: FAIL — `repo.ResetCheckState undefined (type *Topics has no field or method ResetCheckState)`.

- [ ] **Step 3: Write the implementation**

Add to `backend/internal/db/repo/topics.go`, directly after `MarkEpisodeDownloaded`:

```go
// ResetCheckState discards a topic's check/download state so the next check
// re-detects the current release as new and re-delivers it. It is the inverse
// of RecordCheckResult, plus the per-episode progress MarkEpisodeDownloaded
// accumulates in extra.
//
// Configuration is deliberately untouched: client, notifier, category,
// download dir, interval, replace policy, display name, and the capability
// keys in extra (quality, start_season, start_episode, source) all survive.
// Only downloaded_episodes is dropped, via a targeted JSONB key delete rather
// than the whole-blob overwrite UpdateExtra would do.
//
// A paused topic stays paused — only 'error' is normalised back to 'active'.
// Resetting must not silently resume topics the user deliberately stopped,
// which matters most under a bulk reset over a mixed selection.
//
// next_check_at = now() is what "check now" means here: DueForCheck selects on
// it, and there is no separate manual-trigger path in the scheduler.
//
// Returns ErrNotFound when the topic does not exist or belongs to another user.
func (r *Topics) ResetCheckState(ctx context.Context, id, userID uuid.UUID) error {
	const q = `
UPDATE topics SET
    last_hash          = NULL,
    last_checked_at    = NULL,
    last_updated_at    = NULL,
    consecutive_errors = 0,
    last_error         = NULL,
    last_error_code    = '',
    next_check_at      = now(),
    status             = CASE WHEN status = 'paused' THEN 'paused' ELSE 'active' END,
    extra              = COALESCE(extra, '{}'::jsonb) - 'downloaded_episodes',
    updated_at         = now()
WHERE id = $1 AND user_id = $2`
	ct, err := r.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("topics: reset check state: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/db/repo && go test ./internal/db/repo/ -run TestTopics_ResetCheckState -v"
```
Expected: PASS, three tests. `gofmt -l` must print nothing (it exits 0 even when it lists files, so read the output).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/repo/topics.go backend/internal/db/repo/topics_test.go
git commit -m "feat(repo): add Topics.ResetCheckState"
```

---

### Task 2: `Deliveries.DeleteForTopic` repo method

**Files:**
- Modify: `backend/internal/db/repo/deliveries.go` (add after `DeleteByInfohashes`, which ends at line 105)
- Test: `backend/internal/db/repo/deliveries_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (r *Deliveries) DeleteForTopic(ctx context.Context, topicID uuid.UUID) (int64, error)` — returns the number of rows removed.

**Why this is mandatory, not cosmetic:** `topic_deliveries` has a unique index on `(topic_id, infohash)` and `Record` uses `ON CONFLICT DO NOTHING`. A surviving row makes the post-reset re-delivery record a silent no-op, so `GET /topics/{id}/status` would show nothing forever.

`newMockDeliveries` already exists at `deliveries_test.go:15`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/db/repo/deliveries_test.go`:

```go
// ---------- DeleteForTopic ----------

func TestDeliveries_DeleteForTopic_RemovesAllRows(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	topicID := uuid.New()
	mock.ExpectExec(`DELETE FROM topic_deliveries WHERE topic_id = \$1`).
		WithArgs(topicID).
		WillReturnResult(pgxmock.NewResult("DELETE", 3))

	n, err := repo.DeleteForTopic(context.Background(), topicID)
	if err != nil {
		t.Fatalf("DeleteForTopic: unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("DeleteForTopic: want 3 rows, got %d", n)
	}
}

func TestDeliveries_DeleteForTopic_DBError(t *testing.T) {
	repo, mock := newMockDeliveries(t)
	t.Cleanup(func() { assertExpectationsMet(t, mock) })

	dbErr := errors.New("connection refused")
	mock.ExpectExec(`DELETE FROM topic_deliveries`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(dbErr)

	if _, err := repo.DeleteForTopic(context.Background(), uuid.New()); !errors.Is(err, dbErr) {
		t.Fatalf("DeleteForTopic: want wrapped %v, got %v", dbErr, err)
	}
}
```

If `errors` is not yet imported in `deliveries_test.go`, add it to the import block.

- [ ] **Step 2: Run the tests to verify they fail**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test ./internal/db/repo/ -run TestDeliveries_DeleteForTopic -v"
```
Expected: FAIL — `repo.DeleteForTopic undefined`.

- [ ] **Step 3: Write the implementation**

Add to `backend/internal/db/repo/deliveries.go`, after `DeleteByInfohashes`:

```go
// DeleteForTopic removes every delivery row for a topic. Used by the topic
// reset endpoint: the (topic_id, infohash) unique index plus Record's
// ON CONFLICT DO NOTHING means a surviving row would make the post-reset
// re-delivery record a silent no-op, leaving the status view permanently
// empty for that torrent. Returns the number of rows removed.
func (r *Deliveries) DeleteForTopic(ctx context.Context, topicID uuid.UUID) (int64, error) {
	const q = `DELETE FROM topic_deliveries WHERE topic_id = $1`
	ct, err := r.pool.Exec(ctx, q, topicID)
	if err != nil {
		return 0, fmt.Errorf("deliveries: delete for topic: %w", err)
	}
	return ct.RowsAffected(), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/db/repo && go test ./internal/db/repo/ -v"
```
Expected: PASS, all repo tests. `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/db/repo/deliveries.go backend/internal/db/repo/deliveries_test.go
git commit -m "feat(repo): add Deliveries.DeleteForTopic"
```

---

### Task 3: `internal/clientremove` package

**Files:**
- Create: `backend/internal/clientremove/clientremove.go`
- Test: `backend/internal/clientremove/clientremove_test.go`

**Interfaces:**
- Consumes: `registry.Client`, `registry.WithRemoval` (`Remove(ctx, rawConfig []byte, hashes []string, deleteData bool) error`), `domain.Client`, `domain.TopicDelivery`.
- Produces:
  - `type Result struct { ClientID uuid.UUID; ClientName string; Hashes []string; OK bool; Reason string; Err error }`
  - `const ReasonLookup/ReasonNoPlugin/ReasonUnsupported/ReasonDecrypt/ReasonError`
  - `type Remover struct { Clients ClientsLookup; Master Decryptor; Lookup PluginLookup; Timeout time.Duration }`
  - `func New(clients ClientsLookup, master Decryptor, timeout time.Duration) *Remover`
  - `func (r *Remover) Remove(ctx context.Context, userID uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []Result`
  - `func GroupByClient(deliveries []*domain.TopicDelivery) map[uuid.UUID][]string`

This package is created with no callers; Tasks 4 and 6 adopt it.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/clientremove/clientremove_test.go`:

```go
package clientremove

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// fakeClient implements registry.Client but NOT registry.WithRemoval, so it
// stands in for a client that cannot remove torrents (downloadfolder).
type fakeClient struct{ name string }

func (f *fakeClient) Name() string                    { return f.name }
func (f *fakeClient) DisplayName() string             { return f.name }
func (f *fakeClient) ConfigSchema() map[string]any    { return map[string]any{} }
func (f *fakeClient) Test(context.Context, []byte) error { return nil }
func (f *fakeClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}

// fakeRemovalClient additionally implements registry.WithRemoval and records
// the arguments it was called with.
type fakeRemovalClient struct {
	fakeClient
	err        error
	gotHashes  []string
	gotDelete  bool
	gotRawCfg  []byte
	callCount  int
}

func (f *fakeRemovalClient) Remove(_ context.Context, rawConfig []byte, hashes []string, deleteData bool) error {
	f.callCount++
	f.gotRawCfg, f.gotHashes, f.gotDelete = rawConfig, hashes, deleteData
	return f.err
}

type fakeClients struct {
	client *domain.Client
	err    error
}

func (f *fakeClients) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

type fakeDecryptor struct {
	plain []byte
	err   error
}

func (f *fakeDecryptor) Decrypt([]byte, []byte) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.plain, nil
}

func TestRemover_Remove_Success(t *testing.T) {
	plugin := &fakeRemovalClient{fakeClient: fakeClient{name: "qbittorrent"}}
	clientID := uuid.New()
	r := &Remover{
		Clients: &fakeClients{client: &domain.Client{ClientName: "qbittorrent"}},
		Master:  &fakeDecryptor{plain: []byte(`{"url":"http://qb:6611"}`)},
		Lookup:  func(string) registry.Client { return plugin },
		Timeout: time.Second,
	}

	got := r.Remove(context.Background(), uuid.New(),
		map[uuid.UUID][]string{clientID: {"aaa", "bbb"}}, true)

	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	if !got[0].OK || got[0].Reason != "" {
		t.Fatalf("want OK result, got %+v", got[0])
	}
	if got[0].ClientName != "qbittorrent" || got[0].ClientID != clientID {
		t.Fatalf("result identity wrong: %+v", got[0])
	}
	if !plugin.gotDelete {
		t.Fatal("deleteData was not forwarded to the plugin")
	}
	if len(plugin.gotHashes) != 2 {
		t.Fatalf("want 2 hashes forwarded, got %v", plugin.gotHashes)
	}
	if string(plugin.gotRawCfg) != `{"url":"http://qb:6611"}` {
		t.Fatalf("decrypted config not forwarded: %s", plugin.gotRawCfg)
	}
}

func TestRemover_Remove_FailureBranches(t *testing.T) {
	removeErr := errors.New("connection refused")
	lookupErr := errors.New("no such client")
	decryptErr := errors.New("bad nonce")

	tests := []struct {
		name       string
		clients    ClientsLookup
		master     Decryptor
		lookup     PluginLookup
		wantReason string
		wantErr    error
	}{
		{
			name:       "client row missing",
			clients:    &fakeClients{err: lookupErr},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return nil },
			wantReason: ReasonLookup,
			wantErr:    lookupErr,
		},
		{
			name:       "plugin not installed",
			clients:    &fakeClients{client: &domain.Client{ClientName: "gone"}},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return nil },
			wantReason: ReasonNoPlugin,
		},
		{
			name:       "plugin cannot remove",
			clients:    &fakeClients{client: &domain.Client{ClientName: "downloadfolder"}},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup:     func(string) registry.Client { return &fakeClient{name: "downloadfolder"} },
			wantReason: ReasonUnsupported,
		},
		{
			name:       "config undecryptable",
			clients:    &fakeClients{client: &domain.Client{ClientName: "qbittorrent"}},
			master:     &fakeDecryptor{err: decryptErr},
			lookup: func(string) registry.Client {
				return &fakeRemovalClient{fakeClient: fakeClient{name: "qbittorrent"}}
			},
			wantReason: ReasonDecrypt,
			wantErr:    decryptErr,
		},
		{
			name:       "client rejected the call",
			clients:    &fakeClients{client: &domain.Client{ClientName: "transmission"}},
			master:     &fakeDecryptor{plain: []byte("{}")},
			lookup: func(string) registry.Client {
				return &fakeRemovalClient{fakeClient: fakeClient{name: "transmission"}, err: removeErr}
			},
			wantReason: ReasonError,
			wantErr:    removeErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Remover{Clients: tt.clients, Master: tt.master, Lookup: tt.lookup, Timeout: time.Second}
			got := r.Remove(context.Background(), uuid.New(),
				map[uuid.UUID][]string{uuid.New(): {"aaa"}}, false)

			if len(got) != 1 {
				t.Fatalf("want 1 result, got %d", len(got))
			}
			if got[0].OK {
				t.Fatalf("want failure, got OK: %+v", got[0])
			}
			if got[0].Reason != tt.wantReason {
				t.Fatalf("want reason %q, got %q", tt.wantReason, got[0].Reason)
			}
			if tt.wantErr != nil && !errors.Is(got[0].Err, tt.wantErr) {
				t.Fatalf("want err %v, got %v", tt.wantErr, got[0].Err)
			}
			if len(got[0].Hashes) != 1 {
				t.Fatalf("failure result must still carry its hashes, got %v", got[0].Hashes)
			}
		})
	}
}

func TestGroupByClient_SkipsRowsWithoutAClient(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	deliveries := []*domain.TopicDelivery{
		{Infohash: "h1", ClientID: &a},
		{Infohash: "h2", ClientID: &a},
		{Infohash: "h3", ClientID: &b},
		{Infohash: "h4", ClientID: nil}, // unaddressable — no client to remove from
	}

	got := GroupByClient(deliveries)

	if len(got) != 2 {
		t.Fatalf("want 2 clients, got %d (%v)", len(got), got)
	}
	if len(got[a]) != 2 || len(got[b]) != 1 {
		t.Fatalf("grouping wrong: %v", got)
	}
}

func TestGroupByClient_EmptyInput(t *testing.T) {
	if got := GroupByClient(nil); len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test ./internal/clientremove/ -v"
```
Expected: FAIL — build error, `undefined: Remover`, `undefined: GroupByClient`, etc.

- [ ] **Step 3: Write the implementation**

Create `backend/internal/clientremove/clientremove.go`:

```go
// Package clientremove removes torrents from a download client by infohash.
//
// Two callers need the identical sequence — resolve the client row, resolve its
// plugin, assert registry.WithRemoval, decrypt the stored config blob, and call
// Remove under a bounded timeout: the scheduler's replace-on-update policy
// (issue #101) and the topic reset endpoint. It lives here so there is one
// implementation rather than two that drift apart.
//
// The package deliberately does not log or meter. Its two callers report under
// different metric names and log components, so they label their own.
package clientremove

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Reason classifies why a removal did not happen. Empty on success. These
// strings are used as Prometheus label values, so they are a closed set.
const (
	ReasonLookup      = "lookup"      // the client row could not be loaded
	ReasonNoPlugin    = "no_plugin"   // the client's plugin is not installed
	ReasonUnsupported = "unsupported" // the plugin cannot remove torrents
	ReasonDecrypt     = "decrypt"     // the stored config blob is unreadable
	ReasonError       = "error"       // the client rejected or failed the call
)

// ClientsLookup resolves a client row scoped to its owner.
type ClientsLookup interface {
	GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Client, error)
}

// Decryptor unseals a stored client config blob.
type Decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// PluginLookup resolves a client plugin by name. A test seam — production
// passes registry.GetClient.
type PluginLookup func(name string) registry.Client

// Result is the outcome of removing one client's share of the hashes.
type Result struct {
	ClientID   uuid.UUID
	ClientName string   // empty when the client row could not be loaded
	Hashes     []string // the hashes this call covered, success or not
	OK         bool     // true only when the client confirmed the removal
	Reason     string   // "" when OK; one of the Reason* constants otherwise
	Err        error    // the underlying error, when there was one
}

// Remover removes torrents from clients.
type Remover struct {
	Clients ClientsLookup
	Master  Decryptor
	Lookup  PluginLookup
	Timeout time.Duration
}

// New constructs a Remover backed by the global plugin registry.
func New(clients ClientsLookup, master Decryptor, timeout time.Duration) *Remover {
	return &Remover{Clients: clients, Master: master, Lookup: registry.GetClient, Timeout: timeout}
}

// Remove deletes the given infohashes from each client that holds them,
// optionally deleting their on-disk data. byClient maps a client id to the
// hashes delivered through it — deliveries are grouped by the client they
// actually went to, which may differ from the topic's current client if the
// user reassigned it.
//
// Returns one Result per client, in unspecified order. It never returns an
// error: every failure is reported per client so the caller decides whether to
// degrade or abort.
func (r *Remover) Remove(ctx context.Context, userID uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []Result {
	out := make([]Result, 0, len(byClient))
	for clientID, hashes := range byClient {
		out = append(out, r.removeOne(ctx, userID, clientID, hashes, deleteData))
	}
	return out
}

func (r *Remover) removeOne(ctx context.Context, userID, clientID uuid.UUID, hashes []string, deleteData bool) Result {
	res := Result{ClientID: clientID, Hashes: hashes}

	cfg, err := r.Clients.GetByID(ctx, clientID, userID)
	if err != nil {
		res.Reason, res.Err = ReasonLookup, err
		return res
	}
	res.ClientName = cfg.ClientName

	plugin := r.Lookup(cfg.ClientName)
	if plugin == nil {
		res.Reason = ReasonNoPlugin
		return res
	}
	remover, ok := plugin.(registry.WithRemoval)
	if !ok {
		res.Reason = ReasonUnsupported
		return res
	}

	rawConfig, err := r.Master.Decrypt(cfg.ConfigEnc, cfg.ConfigNonce)
	if err != nil {
		res.Reason, res.Err = ReasonDecrypt, err
		return res
	}

	rctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	if err := remover.Remove(rctx, rawConfig, hashes, deleteData); err != nil {
		res.Reason, res.Err = ReasonError, err
		return res
	}
	res.OK = true
	return res
}

// GroupByClient buckets deliveries by the client they were sent to. Rows with
// no client id are skipped — there is no client to address the removal to.
func GroupByClient(deliveries []*domain.TopicDelivery) map[uuid.UUID][]string {
	byClient := map[uuid.UUID][]string{}
	for _, d := range deliveries {
		if d.ClientID == nil {
			continue
		}
		byClient[*d.ClientID] = append(byClient[*d.ClientID], d.Infohash)
	}
	return byClient
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/clientremove && go vet ./internal/clientremove/ && go test -race ./internal/clientremove/ -v"
```
Expected: PASS, all cases. `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/clientremove/
git commit -m "feat: add clientremove package for torrent removal"
```

---

### Task 4: Scheduler adopts `clientremove`

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go` — rewrite `replacePrevious` (lines 892-930), delete `removeFromClient` (lines 932-976)

**Interfaces:**
- Consumes: `clientremove.Remover`, `clientremove.Result`, `clientremove.GroupByClient`, `clientremove.PluginLookup` from Task 3.
- Produces: nothing new for later tasks. This is a refactor with no behaviour change to the check flow.

**Two deliberate metric-label changes** — call them out in the commit body so a reviewer does not read them as regressions:
1. A client-row lookup failure now increments `marauder_scheduler_replaced_previous_total{result="lookup"}`. It previously logged and returned without metering at all.
2. A config decrypt failure is now labelled `result="decrypt"` instead of `result="error"`, so the two genuinely different faults are distinguishable.

Both are additive label values on an existing metric; nothing that previously counted stops counting.

- [ ] **Step 1: Replace `replacePrevious` and delete `removeFromClient`**

In `backend/internal/scheduler/scheduler.go`, replace the whole `replacePrevious` function AND the whole `removeFromClient` function with:

```go
// replacePrevious removes a topic's previously delivered torrents (the
// pre-update snapshot) from the clients that hold them, optionally deleting
// their on-disk data, then prunes the now-stale delivery rows. This is the
// "replace previous version" policy (issue #101) for single-release topics so
// updated releases don't accumulate duplicate downloads and fill the disk.
//
// Best-effort / fail-open throughout: the new release was already delivered
// successfully, so every failure here is logged and metered but never affects
// the check result. Only rows whose client confirmed the removal are pruned —
// an unremoved torrent keeps its delivery record rather than being orphaned.
func (s *Scheduler) replacePrevious(ctx context.Context, log zerolog.Logger, t *domain.Topic, prior []*domain.TopicDelivery, keepHashes []string) {
	// Never remove a torrent that was just (re)delivered this tick: if a tracker
	// bumped its opaque check hash while the torrent itself is unchanged, the
	// "previous" snapshot would contain the current infohash. Infohashes are
	// stored lowercase hex; compare case-insensitively to be safe.
	keep := make(map[string]struct{}, len(keepHashes))
	for _, h := range keepHashes {
		keep[strings.ToLower(h)] = struct{}{}
	}
	candidates := make([]*domain.TopicDelivery, 0, len(prior))
	for _, d := range prior {
		if _, ok := keep[strings.ToLower(d.Infohash)]; ok {
			continue // still the current delivery — never remove it
		}
		candidates = append(candidates, d)
	}

	byClient := clientremove.GroupByClient(candidates)
	if len(byClient) == 0 {
		return
	}

	var removed []string
	for _, res := range s.remover().Remove(ctx, t.UserID, byClient, t.ReplaceDeleteData) {
		// The metric counts torrents (not calls) uniformly across every result
		// label, matching its Help text.
		n := float64(len(res.Hashes))
		if res.OK {
			metrics.SchedulerReplacedPreviousTotal.WithLabelValues(res.ClientName, "ok").Add(n)
			removed = append(removed, res.Hashes...)
			continue
		}
		metrics.SchedulerReplacedPreviousTotal.
			WithLabelValues(clientLabel(res.ClientName), res.Reason).Add(n)
		log.Warn().Err(res.Err).
			Str("client", clientLabel(res.ClientName)).
			Str("reason", res.Reason).
			Msg("replace-on-update: keeping previous version")
	}

	if len(removed) == 0 {
		return
	}
	if _, err := s.deliveries.DeleteByInfohashes(ctx, t.ID, removed); err != nil {
		log.Warn().Err(err).Msg("replace-on-update: prune delivery rows failed")
	}
	log.Info().
		Int("removed", len(removed)).
		Bool("delete_data", t.ReplaceDeleteData).
		Msg("replaced previous version")
}

// remover builds a clientremove.Remover from the scheduler's own dependencies,
// so the lookupClient test seam stays in force for unit tests.
func (s *Scheduler) remover() *clientremove.Remover {
	return &clientremove.Remover{
		Clients: s.clients,
		Master:  s.master,
		Lookup:  clientremove.PluginLookup(s.lookupClient),
		Timeout: s.cfg.TrackerHTTPTimeout,
	}
}

// clientLabel keeps metric and log cardinality bounded when the client row
// could not be loaded and its name is therefore unknown.
func clientLabel(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}
```

Add the import to the block at the top of the file, in the project group:

```go
	"github.com/artyomsv/marauder/backend/internal/clientremove"
```

- [ ] **Step 2: Build and run the scheduler tests**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/scheduler && go build ./... && go vet ./internal/scheduler/ && go test -race ./internal/scheduler/ -v"
```
Expected: PASS, no behaviour change. If a test fails, the refactor changed semantics — fix the refactor, not the test.

- [ ] **Step 3: Run the full backend suite to confirm nothing else moved**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test -race ./..."
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/scheduler/scheduler.go
git commit -m @'
refactor(scheduler): remove torrents via clientremove

replacePrevious now delegates the client lookup, plugin resolution,
config decrypt and bounded Remove call to internal/clientremove, so the
reset endpoint can reuse the same sequence instead of copying it.

Two additive label values on marauder_scheduler_replaced_previous_total:
a client-row lookup failure now counts as result="lookup" (it was
previously unmetered), and a decrypt failure is labelled "decrypt"
rather than being folded into "error".
'@
```

---

### Task 5: `topic.reset` event type

**Files:**
- Modify: `backend/internal/events/event.go` (const block lines 12-22, policy map lines 32-42)
- Test: `backend/internal/events/event_test.go` — create it if it does not exist; if it does, append.

**Interfaces:**
- Produces: `events.TopicReset Type = "topic.reset"` with policy `{Persist: true, Notifiable: false, SSE: true}`.

**Why not notifiable:** the user pressing Reset does not need to be told they pressed Reset. Persisted so the per-topic timeline explains why the delivery list emptied. Because it is not notifiable, it must not appear in `NotifiableTypes()` and therefore never reaches the notifier `EventPicker`.

- [ ] **Step 1: Write the failing test**

Create or append to `backend/internal/events/event_test.go`:

```go
package events

import "testing"

func TestPolicyFor_TopicReset(t *testing.T) {
	p := PolicyFor(TopicReset)
	if !p.Persist {
		t.Error("topic.reset must be persisted so the timeline explains the emptied delivery list")
	}
	if p.Notifiable {
		t.Error("topic.reset must not be notifiable — the user performed the action themselves")
	}
	if !p.SSE {
		t.Error("topic.reset must be pushed over SSE so open tabs refresh")
	}
}

func TestNotifiableTypes_ExcludesTopicReset(t *testing.T) {
	for _, ty := range NotifiableTypes() {
		if ty == TopicReset {
			t.Fatal("topic.reset leaked into the notifier subscription list")
		}
	}
}
```

If the file already declares `package events`, do not duplicate the declaration.

- [ ] **Step 2: Run the test to verify it fails**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test ./internal/events/ -run TopicReset -v"
```
Expected: FAIL — `undefined: TopicReset`.

- [ ] **Step 3: Add the type and policy**

In `backend/internal/events/event.go`, add to the const block after `TopicAdded`:

```go
	TopicReset        Type = "topic.reset"
```

and to the `policies` map after the `TopicAdded` entry:

```go
	TopicReset:        {Persist: true, Notifiable: false, SSE: true},
```

- [ ] **Step 4: Run the test to verify it passes**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/events && go test ./internal/events/ -v"
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/events/
git commit -m "feat(events): add topic.reset event type"
```

---

### Task 6: `POST /topics/{id}/reset` handler

**Files:**
- Modify: `backend/internal/api/handlers/topics.go` — `topicStore` (lines 27-34), `deliveriesStore` (lines 39-41), `Topics` struct (lines 64-74), and a new `Reset` handler after `setStatus` (line 512)
- Modify: `backend/internal/api/handlers/topics_handler_test.go` — `fakeTopicStore` (line 70) gains `ResetCheckState`
- Modify: `backend/internal/api/handlers/topics_status_test.go` — `fakeDeliveriesStore` (line 18) gains `DeleteForTopic`
- Modify: `backend/internal/metrics/metrics.go` — new counter
- Test: `backend/internal/api/handlers/topics_reset_test.go` (create)

**Interfaces:**
- Consumes: `Topics.ResetCheckState` (Task 1), `Deliveries.DeleteForTopic` (Task 2), `clientremove.Remover`/`Result`/`GroupByClient`/`Reason*` (Task 3), `events.TopicReset` (Task 5).
- Produces: `func (h *Topics) Reset(w http.ResponseWriter, r *http.Request)` for the router (Task 7), and `metrics.TopicResetRemovedTotal`.

**Ordering, and which failures abort:**
1. Remove from clients — **fail-open**, every failure becomes a warning.
2. Delete delivery rows — **fail-closed** (500). A surviving row silently breaks future delivery recording, and the topic has not been reset yet, so a retry is safe.
3. Reset topic state — **fail-closed**. Last, because it is the step that arms the scheduler.

- [ ] **Step 1: Add the metric**

In `backend/internal/metrics/metrics.go`, add a new `var` block after the scheduler block that ends at line 116:

```go
// Topic reset metrics ------------------------------------------------------

var (
	// TopicResetRemovedTotal counts torrents the topic reset endpoint removed
	// from a client, partitioned by client and result ("ok", or one of the
	// clientremove failure reasons: lookup / no_plugin / unsupported /
	// decrypt / error).
	TopicResetRemovedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "marauder_topic_reset_removed_total",
			Help: "Number of torrents removed from a client by a topic reset, partitioned by client and result.",
		},
		[]string{"client", "result"},
	)
)
```

- [ ] **Step 2: Extend the existing test fakes so the package still compiles**

`fakeTopicStore` (`topics_handler_test.go:70`) returns a **single** topic from
`getByID *domain.Topic` with `getByIDErr error` — it is not a map. Match that.

Add to `fakeTopicStore`:

```go
// ResetCheckState records (topicID, userID) per call so tests can assert the
// handler reset the right topic for the right owner.
func (f *fakeTopicStore) ResetCheckState(_ context.Context, id, userID uuid.UUID) error {
	f.resetCalls = append(f.resetCalls, [2]uuid.UUID{id, userID})
	return f.resetErr
}
```

and add these two fields to the `fakeTopicStore` struct:

```go
	// Captured ResetCheckState arguments.
	resetCalls [][2]uuid.UUID
	resetErr   error
```

In `backend/internal/api/handlers/topics_status_test.go`, add to `fakeDeliveriesStore`:

```go
func (f *fakeDeliveriesStore) DeleteForTopic(context.Context, uuid.UUID) (int64, error) {
	f.deleted = true
	return int64(len(f.items)), f.deleteErr
}
```

and these two fields to the `fakeDeliveriesStore` struct:

```go
	deleted   bool
	deleteErr error
```

- [ ] **Step 3: Write the failing handler tests**

Create `backend/internal/api/handlers/topics_reset_test.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/api/middleware"
	"github.com/artyomsv/marauder/backend/internal/auth"
	"github.com/artyomsv/marauder/backend/internal/clientremove"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/events"
)

// fakeRemover is a torrentRemover that returns canned results and records the
// arguments it was called with.
type fakeRemover struct {
	results   []clientremove.Result
	gotDelete bool
	gotHashes map[uuid.UUID][]string
	called    bool
}

func (f *fakeRemover) Remove(_ context.Context, _ uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []clientremove.Result {
	f.called = true
	f.gotDelete = deleteData
	f.gotHashes = byClient
	return f.results
}

// resetRequest builds a POST /topics/{id}/reset request carrying the chi URL
// param and an authenticated user, using the same claims-in-context and
// withURLParam helpers the other handler tests in this package use.
func resetRequest(t *testing.T, topicID, userID uuid.UUID, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/topics/"+topicID.String()+"/reset", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxClaims,
		&auth.Claims{UserID: userID.String()}))
	return withURLParam(req, "id", topicID.String())
}

func TestTopicsReset_WipesStateAndForwardsDeleteData(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{
		getByID: &domain.Topic{ID: topicID, UserID: userID, DisplayName: "Show", URL: "https://tracker/1"},
	}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "transmission", Hashes: []string{"aaa"}, OK: true},
	}}
	var emitted []events.Event
	h := &Topics{
		Topics:     store,
		Deliveries: deliveries,
		Remover:    remover,
		Emit:       func(_ context.Context, ev events.Event) { emitted = append(emitted, ev) },
	}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{"delete_data":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Removed != 1 {
		t.Fatalf("want 1 removed, got %d", got.Removed)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("want no warnings, got %v", got.Warnings)
	}
	if !remover.gotDelete {
		t.Error("delete_data was not forwarded to the remover")
	}
	if !deliveries.deleted {
		t.Error("delivery rows were not deleted")
	}
	if len(store.resetCalls) != 1 || store.resetCalls[0] != [2]uuid.UUID{topicID, userID} {
		t.Errorf("ResetCheckState called wrong: %v", store.resetCalls)
	}
	if len(emitted) != 1 || emitted[0].Type != events.TopicReset {
		t.Errorf("want one topic.reset event, got %v", emitted)
	}
}

func TestTopicsReset_OmittedDeleteDataDefaultsToFalse(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{
		{ClientID: clientID, ClientName: "qbittorrent", Hashes: []string{"aaa"}, OK: true},
	}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, ``))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if remover.gotDelete {
		t.Error("delete_data must default to false when the field is omitted")
	}
}

func TestTopicsReset_RemovalFailureStillWipesStateAndWarns(t *testing.T) {
	topicID, userID, clientID := uuid.New(), uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{items: []*domain.TopicDelivery{
		{Infohash: "aaa", ClientID: &clientID},
	}}
	remover := &fakeRemover{results: []clientremove.Result{{
		ClientID:   clientID,
		ClientName: "transmission",
		Hashes:     []string{"aaa"},
		Reason:     clientremove.ReasonError,
		Err:        errors.New("connection refused"),
	}}}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{"delete_data":false}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("reset must be fail-open on removal failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed  int      `json:"removed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Removed != 0 {
		t.Errorf("want 0 removed, got %d", got.Removed)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "transmission") {
		t.Errorf("want a warning naming the client, got %v", got.Warnings)
	}
	if !deliveries.deleted {
		t.Error("delivery rows must be deleted even when removal failed")
	}
	if len(store.resetCalls) != 1 {
		t.Error("topic state must be reset even when removal failed")
	}
}

func TestTopicsReset_ForeignTopicIsNotFound(t *testing.T) {
	topicID, intruder := uuid.New(), uuid.New()
	// GetByID is user-scoped in the real repo, so a foreign topic surfaces as
	// ErrNotFound rather than as a topic with a different UserID.
	store := &fakeTopicStore{getByIDErr: repo.ErrNotFound}
	deliveries := &fakeDeliveriesStore{}
	remover := &fakeRemover{}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: remover}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, intruder, `{}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if remover.called {
		t.Error("must not touch the client for a topic the caller does not own")
	}
	if deliveries.deleted {
		t.Error("must not delete delivery rows for a topic the caller does not own")
	}
	if len(store.resetCalls) != 0 {
		t.Error("must not reset a topic the caller does not own")
	}
}

func TestTopicsReset_DeliveryDeleteFailureAborts(t *testing.T) {
	topicID, userID := uuid.New(), uuid.New()
	store := &fakeTopicStore{getByID: &domain.Topic{ID: topicID, UserID: userID}}
	deliveries := &fakeDeliveriesStore{deleteErr: errors.New("connection refused")}
	h := &Topics{Topics: store, Deliveries: deliveries, Remover: &fakeRemover{}}

	rec := httptest.NewRecorder()
	h.Reset(rec, resetRequest(t, topicID, userID, `{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.resetCalls) != 0 {
		t.Error("topic state must not be reset when the delivery rows survived")
	}
}
```

**Helpers used above already exist in this package** — do not redefine them: `withURLParam(req, key, value)`, `fakeTopicStore` (`topics_handler_test.go:70`), `fakeDeliveriesStore` (`topics_status_test.go:18`), and the claims-in-context idiom `context.WithValue(ctx, middleware.CtxClaims, &auth.Claims{UserID: uid.String()})` (see `clients_categories_test.go:198-201` and `credentials_interactive_test.go:119-125`).

- [ ] **Step 4: Run the tests to verify they fail**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "go test ./internal/api/handlers/ -run TopicsReset -v"
```
Expected: FAIL — `h.Reset undefined`, `unknown field Remover`.

- [ ] **Step 5: Extend the handler seams and struct**

In `backend/internal/api/handlers/topics.go`, add to `topicStore`:

```go
	ResetCheckState(ctx context.Context, id, userID uuid.UUID) error
```

add to `deliveriesStore`:

```go
	DeleteForTopic(ctx context.Context, topicID uuid.UUID) (int64, error)
```

add a new seam above the `Topics` struct:

```go
// torrentRemover is the consumer seam over *clientremove.Remover so handler
// tests can substitute a fake. Nil-safe: an unset remover skips client removal
// and reports it as a warning rather than failing the reset.
type torrentRemover interface {
	Remove(ctx context.Context, userID uuid.UUID, byClient map[uuid.UUID][]string, deleteData bool) []clientremove.Result
}
```

and add the field to the `Topics` struct:

```go
	// Remover removes already-delivered torrents from their client on reset.
	// Nil-safe (see torrentRemover).
	Remover torrentRemover
```

Add the import:

```go
	"github.com/artyomsv/marauder/backend/internal/clientremove"
	"github.com/artyomsv/marauder/backend/internal/metrics"
```

- [ ] **Step 6: Write the handler**

Add to `backend/internal/api/handlers/topics.go`, after `setStatus`:

```go
// resetTopicReq is the POST /topics/{id}/reset body.
type resetTopicReq struct {
	// DeleteData also deletes the torrents' on-disk data. Defaults to false
	// when omitted: deleting data is irreversible, so it is opt-in. It is a
	// per-action choice, deliberately independent of the topic's stored
	// replace_delete_data policy.
	DeleteData bool `json:"delete_data"`
}

// resetTopicResp reports what the reset managed to do. Warnings is never null
// so the frontend can iterate it unconditionally.
type resetTopicResp struct {
	// Removed counts torrents confirmed removed from a client — not delivery
	// rows deleted. The two differ whenever a removal failed, because rows are
	// deleted regardless.
	Removed  int      `json:"removed"`
	Warnings []string `json:"warnings"`
}

// Reset handles POST /topics/{id}/reset. It discards the topic's check and
// download state so the next check re-detects the current release as new and
// re-delivers it, after removing the already-delivered torrents from the
// client(s) that hold them.
//
// Client removal is fail-open: the broken client is usually the reason the
// user is resetting, so a removal failure becomes a warning rather than
// blocking the reset. Everything after it is fail-closed, and the state reset
// runs last because it is the step that arms the scheduler — if an earlier
// step dies, the topic is simply not reset yet and the action can be retried.
func (h *Topics) Reset(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid topic id"))
		return
	}
	var req resetTopicReq
	// An empty or malformed body means "reset with defaults" — delete_data
	// false. The only field is a bool, so there is nothing to reject.
	_ = json.NewDecoder(r.Body).Decode(&req)

	topic, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil {
		if errors.Is(gerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(gerr.Error()))
		return
	}

	removed, warnings := h.removeDeliveredTorrents(r.Context(), topic, uid, req.DeleteData)

	if h.Deliveries != nil {
		if _, derr := h.Deliveries.DeleteForTopic(r.Context(), id); derr != nil {
			// Fail-closed: a surviving row makes the re-delivery record a
			// silent no-op (unique index + ON CONFLICT DO NOTHING), so the
			// topic must not be armed for a re-check.
			problem.Write(w, r, h.BaseURL, problem.ErrInternal(derr.Error()))
			return
		}
	}

	if rerr := h.Topics.ResetCheckState(r.Context(), id, uid); rerr != nil {
		if errors.Is(rerr, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(rerr.Error()))
		return
	}

	if h.Emit != nil {
		h.Emit(r.Context(), events.Event{
			UserID:     topic.UserID,
			TopicID:    &topic.ID,
			NotifierID: topic.NotifierID,
			Type:       events.TopicReset,
			Severity:   "info",
			Title:      topic.DisplayName,
			Body:       "Topic reset — will re-download from scratch",
			Link:       h.BaseURL + "/topics",
			SourceURL:  topic.URL,
		})
	}

	writeJSON(w, http.StatusOK, resetTopicResp{Removed: removed, Warnings: warnings})
}

// removeDeliveredTorrents removes every torrent the topic has delivered from
// the client it was delivered to. Returns the number of torrents confirmed
// removed and a human-readable warning per client that refused or could not be
// reached. Never fails the reset.
func (h *Topics) removeDeliveredTorrents(ctx context.Context, topic *domain.Topic, uid uuid.UUID, deleteData bool) (int, []string) {
	warnings := []string{}
	if h.Deliveries == nil {
		return 0, warnings
	}
	deliveries, err := h.Deliveries.ListForTopic(ctx, topic.ID)
	if err != nil {
		return 0, append(warnings, "could not list delivered torrents: "+err.Error())
	}
	byClient := clientremove.GroupByClient(deliveries)
	if len(byClient) == 0 {
		return 0, warnings
	}
	if h.Remover == nil {
		return 0, append(warnings, "torrent removal is not configured; the old torrents were left in the client")
	}

	removed := 0
	for _, res := range h.Remover.Remove(ctx, uid, byClient, deleteData) {
		n := float64(len(res.Hashes))
		name := res.ClientName
		if name == "" {
			name = "unknown"
		}
		if res.OK {
			removed += len(res.Hashes)
			metrics.TopicResetRemovedTotal.WithLabelValues(name, "ok").Add(n)
			continue
		}
		metrics.TopicResetRemovedTotal.WithLabelValues(name, res.Reason).Add(n)
		warnings = append(warnings, name+": "+removalWarning(res))
	}
	return removed, warnings
}

// removalWarning renders one failed removal as a sentence the user can act on.
func removalWarning(res clientremove.Result) string {
	switch res.Reason {
	case clientremove.ReasonLookup:
		return "the client this torrent was sent to no longer exists; it was left in place"
	case clientremove.ReasonNoPlugin:
		return "this client's plugin is not installed; the torrent was left in place"
	case clientremove.ReasonUnsupported:
		return "this client cannot remove torrents; remove it there by hand"
	case clientremove.ReasonDecrypt:
		return "the stored client config could not be read; the torrent was left in place"
	default:
		if res.Err != nil {
			return "removal failed: " + res.Err.Error()
		}
		return "removal failed"
	}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal/api/handlers ./internal/metrics && go vet ./internal/api/... && go test -race ./internal/api/... -v"
```
Expected: PASS, including the five new tests and every pre-existing handler test.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/handlers/ backend/internal/metrics/metrics.go
git commit -m @'
feat(api): add POST /topics/{id}/reset handler

Discards a topic's check and download state, removes the already
delivered torrents from the clients holding them, and deletes the
delivery rows so the next check re-delivers from scratch.

Client removal is fail-open and reported as warnings in the 200 body:
a broken client is usually why the reset is being requested, so it must
not block the reset. Delivery-row deletion and the state reset are
fail-closed, with the state reset last so a partial failure leaves the
topic simply un-reset rather than armed and half-cleaned.
'@
```

---

### Task 7: Route and dependency wiring

**Files:**
- Modify: `backend/internal/api/router.go` — `topicsH` construction (lines 97-105), route table (after line 183)

**Interfaces:**
- Consumes: `handlers.Topics.Reset` (Task 6), `clientremove.New` (Task 3).
- Produces: a reachable `POST /api/v1/topics/{id}/reset`.

- [ ] **Step 1: Wire the remover into the handler**

In `backend/internal/api/router.go`, extend the `topicsH` literal:

```go
	topicsH := &handlers.Topics{
		Topics:     d.Topics,
		Deliveries: d.Deliveries,
		Clients:    d.Clients,
		Notifiers:  d.Notifiers,
		Master:     d.Master,
		Remover:    clientremove.New(d.Clients, d.Master, d.Cfg.TrackerHTTPTimeout),
		BaseURL:    d.Cfg.PublicBaseURL,
		Emit:       d.Emit,
	}
```

Add the import:

```go
	"github.com/artyomsv/marauder/backend/internal/clientremove"
```

- [ ] **Step 2: Register the route**

In the same file, immediately after the `/topics/{id}/resume` line:

```go
			r.Post("/topics/{id}/reset", topicsH.Reset)
```

- [ ] **Step 3: Build and run the whole backend suite**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal && go build ./... && go vet ./... && go test -race ./..."
```
Expected: build OK, vet clean, all tests PASS, `gofmt -l` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/router.go
git commit -m "feat(api): register POST /topics/{id}/reset route"
```

---

### Task 8: Frontend API client

**Files:**
- Modify: `frontend/src/lib/api.ts` — add the type near the other topic types, and the method inside the `api` object next to `topicStatus` (line 172)

**Interfaces:**
- Consumes: `POST /topics/{id}/reset` (Task 7).
- Produces: `interface TopicResetResult { removed: number; warnings: string[] }` and `api.resetTopic(id: string, deleteData: boolean): Promise<TopicResetResult>`.

- [ ] **Step 1: Add the result type**

In `frontend/src/lib/api.ts`, add near the other topic-related types (beside `TopicStatus`, around line 300):

```ts
// Result of POST /topics/{id}/reset. `removed` counts torrents confirmed
// removed from a client, which is not the same as the number of delivery rows
// deleted: rows are deleted regardless so the topic can re-record its
// deliveries, while a removal can fail (client down, torrent already gone).
// Every such failure arrives in `warnings` rather than as an error.
export interface TopicResetResult {
  removed: number;
  warnings: string[];
}
```

- [ ] **Step 2: Add the method**

In the `api` object, directly after `topicStatus`:

```ts
  // POST /topics/{id}/reset — discard the topic's check/download state so the
  // next check re-delivers the current release from scratch. `deleteData` also
  // deletes the old torrents' files. Removal failures come back as warnings in
  // a 200, not as errors.
  resetTopic: (id: string, deleteData: boolean) =>
    request<TopicResetResult>("POST", `/topics/${id}/reset`, {
      delete_data: deleteData,
    }),
```

- [ ] **Step 3: Typecheck**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit"
```
Expected: no output (clean).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api.ts
git commit -m "feat(frontend): add resetTopic API client method"
```

---

### Task 9: `ResetTopicCard` component

**Files:**
- Create: `frontend/src/components/topics/ResetTopicCard.tsx`
- Create: `frontend/src/components/topics/ResetTopicCard.test.tsx`
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`
- Modify: `frontend/src/lib/events.ts` — add the `topic.reset` label entry

**Interfaces:**
- Consumes: `api.resetTopic` + `TopicResetResult` (Task 8), `Topic` from `@/lib/api`.
- Produces:
  ```ts
  interface ResetTopicCardProps {
    topics: Topic[];       // one entry for a row reset, many for a bulk reset
    onClose: () => void;   // dismiss the card
    onDone: () => void;    // reset finished — parent invalidates queries
  }
  export function ResetTopicCard(props: ResetTopicCardProps): JSX.Element
  ```

**Design notes:** this is **not** a modal — `components/ui/` has no `Dialog` primitive (only button, input, label, card, badge). It follows `AddTopicCard` / `EditTopicCard`: an inline `<Card>` animated in by the parent's `AnimatePresence`. It does not auto-dismiss after running; it switches to a result view holding the warnings until the user closes it, because this frontend has no toast layer.

Per-topic failures must not abort the rest of a bulk reset — each call is caught individually and turned into a warning.

- [ ] **Step 1: Add the i18n keys**

In `frontend/src/i18n/en.ts`, add beside the other `topics.*` keys:

```ts
  "topics.reset.action": "Reset topic",
  "topics.reset.title": "Reset {name}",
  "topics.reset.titleBulk": "Reset {count} topics",
  "topics.reset.body":
    "Discards delivery records, downloaded-episode progress and error state, then checks again immediately so everything is downloaded from scratch. Settings and history are kept. A paused topic stays paused.",
  "topics.reset.deleteData": "Also delete the downloaded files from the client",
  "topics.reset.confirm": "Reset",
  "topics.reset.cancel": "Cancel",
  "topics.reset.pending": "Resetting…",
  "topics.reset.done": "Removed {count} torrent(s). Queued for a fresh check.",
  "topics.reset.warnings": "Some torrents could not be removed:",
  "topics.reset.close": "Close",
  "events.topic_reset": "Topic reset",
```

In `frontend/src/i18n/ru.ts`, add the same keys:

```ts
  "topics.reset.action": "Сбросить топик",
  "topics.reset.title": "Сбросить «{name}»",
  "topics.reset.titleBulk": "Сбросить топиков: {count}",
  "topics.reset.body":
    "Удаляет записи о загрузках, прогресс по сериям и состояние ошибки, затем сразу проверяет топик заново, чтобы всё скачалось с нуля. Настройки и история сохраняются. Приостановленный топик останется приостановленным.",
  "topics.reset.deleteData": "Также удалить скачанные файлы из клиента",
  "topics.reset.confirm": "Сбросить",
  "topics.reset.cancel": "Отмена",
  "topics.reset.pending": "Сброс…",
  "topics.reset.done": "Удалено торрентов: {count}. Поставлено в очередь на проверку.",
  "topics.reset.warnings": "Некоторые торренты удалить не удалось:",
  "topics.reset.close": "Закрыть",
  "events.topic_reset": "Топик сброшен",
```

In `frontend/src/lib/events.ts`, add to `EVENT_LABELS`, after the `"topic.added"` line:

```ts
  "topic.reset": "events.topic_reset",
```

- [ ] **Step 1b: Route the live `topic.reset` event**

The tab that pressed Reset refreshes itself via `onDone` (Task 10), but a second
tab or another session only learns about it over SSE. In
`frontend/src/lib/events-stream.ts`, find the `applyEvent` arm that invalidates
on `release.found` / `download.submitted` / `topic.added` and add
`"topic.reset"` to the same list — a reset changes the topic row, its delivery
list and its timeline, which is exactly what those invalidations cover.

Open the file first and match its existing shape (a `switch`, a set membership
test, or an array of types) rather than assuming; add the type to whichever
construct is there.

- [ ] **Step 2: Write the failing tests**

Create `frontend/src/components/topics/ResetTopicCard.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { ResetTopicCard } from "./ResetTopicCard";
import { api, type Topic } from "@/lib/api";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { ...actual.api, resetTopic: vi.fn() } };
});

const topic = (id: string, name: string) => ({ ID: id, DisplayName: name }) as Topic;

function renderCard(topics: Topic[], onDone = vi.fn(), onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ResetTopicCard topics={topics} onClose={onClose} onDone={onDone} />
    </QueryClientProvider>,
  );
  return { onDone, onClose };
}

describe("ResetTopicCard", () => {
  beforeEach(() => {
    vi.mocked(api.resetTopic).mockReset();
  });

  it("defaults the delete-data checkbox to off and forwards its value", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({ removed: 1, warnings: [] });
    renderCard([topic("t1", "Show")]);

    const checkbox = screen.getByRole("checkbox");
    expect(checkbox).not.toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));
    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledWith("t1", false));
  });

  it("forwards delete_data when the checkbox is ticked", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({ removed: 1, warnings: [] });
    renderCard([topic("t1", "Show")]);

    await userEvent.click(screen.getByRole("checkbox"));
    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledWith("t1", true));
  });

  it("shows the result and its warnings without auto-closing", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({
      removed: 2,
      warnings: ["transmission: removal failed: connection refused"],
    });
    const { onClose } = renderCard([topic("t1", "Show")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    expect(await screen.findByText(/removed 2 torrent/i)).toBeInTheDocument();
    expect(screen.getByText(/connection refused/i)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("resets every selected topic and aggregates the results", async () => {
    vi.mocked(api.resetTopic)
      .mockResolvedValueOnce({ removed: 1, warnings: [] })
      .mockResolvedValueOnce({ removed: 2, warnings: [] });
    const { onDone } = renderCard([topic("t1", "A"), topic("t2", "B")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledTimes(2));
    expect(await screen.findByText(/removed 3 torrent/i)).toBeInTheDocument();
    expect(onDone).toHaveBeenCalled();
  });

  it("turns a failed topic into a warning instead of losing the others", async () => {
    vi.mocked(api.resetTopic)
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce({ removed: 1, warnings: [] });
    renderCard([topic("t1", "A"), topic("t2", "B")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    expect(await screen.findByText(/A: boom/)).toBeInTheDocument();
    expect(screen.getByText(/removed 1 torrent/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx vitest run src/components/topics/ResetTopicCard.test.tsx"
```
Expected: FAIL — cannot resolve `./ResetTopicCard`.

- [ ] **Step 4: Write the component**

Create `frontend/src/components/topics/ResetTopicCard.tsx`:

```tsx
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { AlertTriangle, RotateCcw } from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useT } from "@/i18n";

interface ResetTopicCardProps {
  // One entry for a row reset, many for a bulk reset.
  topics: Topic[];
  onClose: () => void;
  // Called once the reset finishes so the page can invalidate its queries and
  // clear its selection. The card deliberately stays open afterwards to show
  // the result — this app has no toast layer, so a client failure would
  // otherwise disappear unseen.
  onDone: () => void;
}

interface ResetOutcome {
  removed: number;
  warnings: string[];
}

export function ResetTopicCard({ topics, onClose, onDone }: ResetTopicCardProps) {
  const t = useT();
  const [deleteData, setDeleteData] = useState(false);
  const [outcome, setOutcome] = useState<ResetOutcome | null>(null);

  const reset = useMutation({
    mutationFn: async (): Promise<ResetOutcome> => {
      // Each topic is caught individually: one unreachable client must not
      // discard the results of every other topic in a bulk reset.
      const results = await Promise.all(
        topics.map(async (topic) => {
          try {
            const res = await api.resetTopic(topic.ID, deleteData);
            return {
              removed: res.removed,
              warnings: res.warnings.map((w) => `${topic.DisplayName}: ${w}`),
            };
          } catch (err) {
            const message = err instanceof Error ? err.message : "reset failed";
            return { removed: 0, warnings: [`${topic.DisplayName}: ${message}`] };
          }
        }),
      );
      return {
        removed: results.reduce((sum, r) => sum + r.removed, 0),
        warnings: results.flatMap((r) => r.warnings),
      };
    },
    onSuccess: (res) => {
      setOutcome(res);
      onDone();
    },
  });

  const bulk = topics.length > 1;
  const title = bulk
    ? t("topics.reset.titleBulk", { count: topics.length })
    : t("topics.reset.title", { name: topics[0]?.DisplayName ?? "" });

  return (
    <motion.div
      initial={{ opacity: 0, y: -8, height: 0 }}
      animate={{ opacity: 1, y: 0, height: "auto" }}
      exit={{ opacity: 0, y: -8, height: 0 }}
      transition={{ duration: 0.2 }}
    >
      <Card className="overflow-hidden p-4">
        <div className="flex items-center gap-2 font-medium">
          <RotateCcw className="size-4 text-primary" />
          {title}
        </div>

        {outcome ? (
          <ResetResult outcome={outcome} onClose={onClose} />
        ) : (
          <>
            <p className="mt-2 text-sm text-muted-foreground">{t("topics.reset.body")}</p>
            <label className="mt-3 flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-4 cursor-pointer"
                checked={deleteData}
                onChange={(e) => setDeleteData(e.target.checked)}
              />
              {t("topics.reset.deleteData")}
            </label>
            <div className="mt-4 flex items-center gap-2">
              <Button
                variant="destructive"
                size="sm"
                disabled={reset.isPending}
                onClick={() => reset.mutate()}
              >
                {reset.isPending ? t("topics.reset.pending") : t("topics.reset.confirm")}
              </Button>
              <Button variant="ghost" size="sm" onClick={onClose} disabled={reset.isPending}>
                {t("topics.reset.cancel")}
              </Button>
            </div>
          </>
        )}
      </Card>
    </motion.div>
  );
}

function ResetResult({ outcome, onClose }: { outcome: ResetOutcome; onClose: () => void }) {
  const t = useT();
  return (
    <>
      <p className="mt-2 text-sm">{t("topics.reset.done", { count: outcome.removed })}</p>
      {outcome.warnings.length > 0 && (
        <div className="mt-3 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
          <div className="flex items-center gap-2 font-medium">
            <AlertTriangle className="size-4" />
            {t("topics.reset.warnings")}
          </div>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
            {outcome.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="mt-4">
        <Button variant="outline" size="sm" onClick={onClose}>
          {t("topics.reset.close")}
        </Button>
      </div>
    </>
  );
}
```

The `warning` / `warning-foreground` Tailwind tokens are declared in `frontend/src/index.css` (light at lines 38-39, dark at 76-77, exposed as `--color-warning` at line 139), so `border-warning/40 bg-warning/10` resolves in both themes.

- [ ] **Step 5: Run the tests to verify they pass**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run src/components/topics/ResetTopicCard.test.tsx"
```
Expected: typecheck clean, five tests PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/topics/ResetTopicCard.tsx frontend/src/components/topics/ResetTopicCard.test.tsx frontend/src/i18n/ frontend/src/lib/events.ts frontend/src/lib/events-stream.ts
git commit -m "feat(frontend): add ResetTopicCard confirm and result card"
```

---

### Task 10: Extract `Topics.tsx` sub-components (pure refactor)

**Files:**
- Create: `frontend/src/components/topics/TopicRow.tsx`, `BulkActionBar.tsx`, `DensityToggle.tsx`, `TopicsEmptyState.tsx`, `StatusIndicator.tsx`
- Modify: `frontend/src/pages/Topics.tsx` — remove the five local components (lines 280-425) and the inline row markup (lines 188-271), import the extracted ones

**Interfaces:**
- Consumes: `Topic` from `@/lib/api`, `ClientRef` from `@/components/topics/ClientBadge`, `NotifierRef` from `@/components/topics/NotifierBadge`.
- Produces, for Task 11:
  ```ts
  // TopicRow.tsx
  export interface TopicRowLookups {
    clientById: Map<string, ClientRef>;
    defaultClient: ClientRef | null;
    notifierById: Map<string, NotifierRef>;
  }
  export interface TopicRowActions {
    onToggleSelect: () => void;
    onEdit: () => void;
    onDelete: () => void;
  }
  export interface TopicRowProps {
    topic: Topic;
    compact: boolean;
    selected: boolean;
    deletePending: boolean;
    lookups: TopicRowLookups;
    actions: TopicRowActions;
  }
  export function TopicRow(props: TopicRowProps): JSX.Element

  // BulkActionBar.tsx — same props as the current local component
  export interface BulkActionBarProps {
    count: number;
    onPause: () => void;
    onResume: () => void;
    onDelete: () => void;
    onClear: () => void;
  }
  export function BulkActionBar(props: BulkActionBarProps): JSX.Element

  // DensityToggle.tsx
  export function DensityToggle(props: {
    density: "comfortable" | "compact";
    setDensity: (d: "comfortable" | "compact") => void;
  }): JSX.Element

  // TopicsEmptyState.tsx
  export function TopicsEmptyState(props: { onAdd: () => void }): JSX.Element

  // StatusIndicator.tsx
  export function StatusIndicator(props: { status: Topic["Status"] }): JSX.Element
  ```

**Why this task exists:** `Topics.tsx` is 425 lines against this project's
250-line component limit — a pre-existing breach tracked in `techdebt/frontend/`.
Task 11 modifies exactly the parts of this file that are over the line, so the
extraction is paid down here first, as a **separate commit with no behaviour
change**, rather than making the breach worse. Keeping it separate also means
Task 11's diff shows only the reset feature.

**Hard rule for this task: no behaviour changes.** Move the JSX and its
Tailwind classes verbatim. Do not rename props, reorder elements, "improve"
class names, or add the reset button (that is Task 11). The existing tests are
the guard: they must pass untouched.

**Prop grouping:** `TopicRow` takes `lookups` and `actions` objects rather than
eleven flat props, because this project caps components at 8 props. Keep that
shape — Task 11 adds `onReset` inside `actions`, which stays within the cap.

- [ ] **Step 1: Extract the four leaf components**

Create one file per component under `frontend/src/components/topics/`, moving
the function bodies **verbatim** from `Topics.tsx`:

- `DensityToggle.tsx` ← `DensityToggle` (Topics.tsx:280-317). Needs `Rows3`, `Rows4` from lucide-react, `cn` from `@/lib/utils`.
- `TopicsEmptyState.tsx` ← `EmptyState` (Topics.tsx:392-408), renamed to `TopicsEmptyState` because the bare name is too generic for a shared directory. Needs `Plus` from lucide-react, `Button` from `@/components/ui/button`.
- `StatusIndicator.tsx` ← `StatusIndicator` (Topics.tsx:410-425). Needs `type Topic` from `@/lib/api`.
- `BulkActionBar.tsx` ← `BulkActionBarProps` + `BulkActionBar` (Topics.tsx:319-390). Needs `motion` from framer-motion, `Pause`/`Play`/`Trash2`/`Check`/`X` from lucide-react, `Button`, and `useArmedConfirm` from `@/lib/hooks/useArmedConfirm`. Export the props interface.

Each file exports its component (and `BulkActionBar` also its props interface).

- [ ] **Step 2: Extract `TopicRow`**

Create `frontend/src/components/topics/TopicRow.tsx` holding the entire
`motion.div` currently inlined at `Topics.tsx:189-270`, with the interfaces
listed under **Produces** above. Inside the component, replace the closed-over
values with props:

- `t` → `topic`
- `selected.has(t.ID)` → `selected`
- `toggleOne(t.ID)` → `actions.onToggleSelect()`
- `setEditing(t)` → `actions.onEdit()`
- `del.mutate(t.ID)` → `actions.onDelete()`
- `del.isPending && del.variables === t.ID` → `deletePending`
- `clientById` / `defaultClient` / `notifierById` → `lookups.*`

Keep `key={t.ID}` on the **caller** side, not inside the component. Move the
imports it needs (`motion`, `Badge`, `Button`, `Pencil`, `cn`, `formatRelative`,
`DeleteConfirm`, `PosterImage`, `TopicUrl`, `TopicError`, `DeliveryStatus`,
`TopicCheckStatus`, `TopicHistoryDisclosure`, `ClientBadge`, `NotifierBadge`,
`SonarrBadge`, `StatusIndicator`) into the new file and drop the ones
`Topics.tsx` no longer uses.

- [ ] **Step 3: Rewrite the `Topics.tsx` render to use them**

The list body becomes:

```tsx
            <div className="divide-y divide-border/60">
              {topics.map((t) => (
                <TopicRow
                  key={t.ID}
                  topic={t}
                  compact={compact}
                  selected={selected.has(t.ID)}
                  deletePending={del.isPending && del.variables === t.ID}
                  lookups={{ clientById, defaultClient, notifierById }}
                  actions={{
                    onToggleSelect: () => toggleOne(t.ID),
                    onEdit: () => setEditing(t),
                    onDelete: () => del.mutate(t.ID),
                  }}
                />
              ))}
            </div>
```

and `<EmptyState onAdd={...} />` becomes `<TopicsEmptyState onAdd={...} />`.

Keep the `export { AddTopicCard }` re-export line at the top of `Topics.tsx` —
existing imports and tests depend on it.

- [ ] **Step 4: Verify nothing changed**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: typecheck clean and the whole existing suite PASS **without any test
being edited**. If a test needed changing, the extraction changed behaviour —
fix the extraction.

Then confirm the file actually shrank:

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" node:22-alpine \
  sh -c "wc -l /host/src/pages/Topics.tsx /host/src/components/topics/TopicRow.tsx /host/src/components/topics/BulkActionBar.tsx"
```
Expected: `Topics.tsx` under 250 lines; each new file well under it.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/Topics.tsx frontend/src/components/topics/
git commit -m @'
refactor(frontend): split Topics page into row and chrome components

Topics.tsx was 425 lines against the project's 250-line component limit.
Extract TopicRow, BulkActionBar, DensityToggle, TopicsEmptyState and
StatusIndicator into components/topics/ so the page is a composition
rather than a monolith, ahead of adding the reset action to it.

No behaviour change: markup and classes moved verbatim and the existing
tests pass unedited. TopicRow takes grouped lookups/actions objects to
stay inside the 8-prop limit.
'@
```

---

### Task 11: Wire Reset into the Topics page

**Files:**
- Modify: `frontend/src/pages/Topics.tsx` — imports, page state, `AnimatePresence` block, `BulkActionBar` render
- Modify: `frontend/src/components/topics/TopicRow.tsx` — reset button in the action group, `onReset` in `TopicRowActions`
- Modify: `frontend/src/components/topics/BulkActionBar.tsx` — `onReset` prop + Reset button
- Test: `frontend/src/pages/Topics.test.tsx` — create if absent; if present, append

**Interfaces:**
- Consumes: `ResetTopicCard` (Task 9), `useCheckStatus` (existing), `QK` (existing).
- Produces: nothing for later tasks.

**State shape:** a new `resetting: Topic[] | null` held separately from `selected`. It must be separate — `onDone` clears `selected`, and if the card's topics were derived from `selected` the card would unmount before showing its result.

- [ ] **Step 1: Add the state, mutation-free handlers, and the card**

In `frontend/src/pages/Topics.tsx`, add:

```tsx
import { ResetTopicCard } from "@/components/topics/ResetTopicCard";
```

Add beside the other page state (after the `selected` line):

```tsx
  // Topics currently being reset. Held separately from `selected` because
  // finishing a bulk reset clears the selection, and the card must stay
  // mounted afterwards to show its result.
  const [resetting, setResetting] = useState<Topic[] | null>(null);
```

Add the completion handler next to `bulk`:

```tsx
  const onResetDone = () => {
    for (const topic of resetting ?? []) {
      useCheckStatus.getState().clear(topic.ID);
      qc.invalidateQueries({ queryKey: QK.topicStatus(topic.ID) });
      qc.invalidateQueries({ queryKey: QK.topicEvents(topic.ID) });
    }
    qc.invalidateQueries({ queryKey: QK.topics });
    setSelected(new Set());
  };
```

Add inside the existing `<AnimatePresence>` block, after the `editing` branch:

```tsx
        {resetting && (
          <ResetTopicCard
            key={resetting.map((t) => t.ID).join(",")}
            topics={resetting}
            onClose={() => setResetting(null)}
            onDone={onResetDone}
          />
        )}
```

- [ ] **Step 2: Add the row reset button**

In `frontend/src/components/topics/TopicRow.tsx`, add `onReset` to
`TopicRowActions`:

```ts
export interface TopicRowActions {
  onToggleSelect: () => void;
  onEdit: () => void;
  onReset: () => void;
  onDelete: () => void;
}
```

import `RotateCcw` from lucide-react, and add the button to the hover action
group, between the Edit button and `DeleteConfirm`:

```tsx
        <Button
          variant="ghost"
          size="icon"
          aria-label="Reset topic"
          onClick={actions.onReset}
        >
          <RotateCcw className="size-4" />
        </Button>
```

Then pass it from `Topics.tsx`, inside the `actions` object of the `TopicRow`
render:

```tsx
                    onReset: () => setResetting([t]),
```

- [ ] **Step 3: Add the bulk reset action**

In `frontend/src/components/topics/BulkActionBar.tsx`, import `RotateCcw`, add
`onReset: () => void;` to `BulkActionBarProps` (after `onResume`), destructure
it, and add the button after Resume, before the delete arm:

```tsx
        <Button variant="outline" size="sm" onClick={onReset}>
          <RotateCcw className="size-4" />
          Reset
        </Button>
```

The bulk Reset button does **not** use `useArmedConfirm` — the `ResetTopicCard`
it opens is itself the confirmation step, with a checkbox to read. Arming twice
would be friction, not safety.

Then pass the prop where `Topics.tsx` renders `BulkActionBar`:

```tsx
          onReset={() => setResetting(topics.filter((t) => selected.has(t.ID)))}
```

- [ ] **Step 4: Write the page tests**

Create (or append to) `frontend/src/pages/Topics.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { TopicsPage } from "./Topics";
import { api } from "@/lib/api";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: { ...actual.api, get: vi.fn(), resetTopic: vi.fn() },
  };
});

const TOPICS = [
  { ID: "t1", DisplayName: "Alpha", TrackerName: "rutracker", Status: "active", URL: "https://x/1" },
  { ID: "t2", DisplayName: "Beta", TrackerName: "rutracker", Status: "active", URL: "https://x/2" },
];

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TopicsPage />
    </QueryClientProvider>,
  );
}

describe("TopicsPage reset", () => {
  beforeEach(() => {
    vi.mocked(api.resetTopic).mockReset();
    vi.mocked(api.get).mockImplementation(async (path: string) => {
      if (path === "/topics") return { topics: TOPICS };
      if (path === "/clients") return { clients: [] };
      if (path === "/notifiers") return { notifiers: [] };
      return {};
    });
  });

  it("opens the reset card for a single topic from the row action", async () => {
    renderPage();
    await screen.findByText("Alpha");

    await userEvent.click(screen.getAllByRole("button", { name: /reset topic/i })[0]);

    expect(await screen.findByText(/Reset Alpha/)).toBeInTheDocument();
  });

  it("resets every selected topic from the bulk bar", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({ removed: 0, warnings: [] });
    renderPage();
    await screen.findByText("Alpha");

    await userEvent.click(screen.getByLabelText("Select all"));
    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));
    await screen.findByText(/Reset 2 topics/);
    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.resetTopic).mock.calls.map((c) => c[0]).sort()).toEqual(["t1", "t2"]);
  });
});
```

If the bulk bar's "Reset" button and the card's "Reset" confirm button collide on the same accessible name in the second test, disambiguate by scoping the second query to the card (e.g. `within(screen.getByText(/Reset 2 topics/).closest("div")!)`) rather than loosening the regex.

- [ ] **Step 5: Run the tests**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: typecheck clean, the whole frontend suite PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/Topics.tsx frontend/src/pages/Topics.test.tsx frontend/src/components/topics/TopicRow.tsx frontend/src/components/topics/BulkActionBar.tsx
git commit -m "feat(frontend): add per-topic and bulk reset actions"
```

---

### Task 12: Documentation

**Files:**
- Modify: `CLAUDE.md` — the backend package table, the scheduler section, the frontend tree
- Modify: `CHANGELOG.md` — the `[Unreleased]` section

**Interfaces:** none.

`CLAUDE.md` must be updated in the same change as the structural work it describes (project rule), and `CHANGELOG.md`'s `[Unreleased]` section becomes the release notes when auto-release next cuts a version.

- [ ] **Step 1: Add the `clientremove` package row to the CLAUDE.md backend table**

Insert alphabetically (after `cfsolver`, before `config`):

```
| **`clientremove`** | shared torrent removal: resolve client → plugin → decrypt config → bounded `registry.WithRemoval.Remove`, returning one `Result{OK, Reason, Err}` per client (`lookup`/`no_plugin`/`unsupported`/`decrypt`/`error`). Used by BOTH the scheduler's replace-on-update policy (issue #101) and the topic reset endpoint, so the sequence exists once. Deliberately does not log or meter — callers label their own metric (`marauder_scheduler_replaced_previous_total` vs `marauder_topic_reset_removed_total`). `GroupByClient` buckets deliveries by the client they actually went to, skipping rows with no client id |
```

- [ ] **Step 2: Document the reset endpoint in the `db/repo` and `events` rows**

In the `db` / `db/repo` row, after the `Deliveries` description, add:

> `DeleteForTopic(topic)` drops every delivery row for a topic (topic reset) — mandatory there, since the `(topic_id, infohash)` unique index would otherwise make the re-delivery `Record` a silent no-op. `Topics` also gains `ResetCheckState(id,user)`: the inverse of `RecordCheckResult` plus an `extra - 'downloaded_episodes'` JSONB key delete, with `next_check_at = now()` to queue an immediate re-check and a `status` CASE that leaves a `paused` topic paused.

In the `events` row, add `topic.reset` to the `Type` list and note it is `Persist:true, Notifiable:false, SSE:true`.

- [ ] **Step 3: Add a reset paragraph to the scheduler/API section**

Add after the "Replace-on-update (issue #101)" paragraph:

```markdown
**Topic reset:** `POST /api/v1/topics/{id}/reset` (body `{"delete_data": bool}`,
default false) discards a topic's check/download state so the next tick
re-delivers the current release from scratch: it removes the already-delivered
torrents from the clients that hold them (via `clientremove`, optionally
deleting data), deletes the topic's `topic_deliveries` rows, then calls
`Topics.ResetCheckState`. Client removal is **fail-open** — the broken client is
usually why the reset was requested — and every failure comes back in the 200
body's `warnings` array; the row delete and the state reset are fail-closed, and
the state reset runs last because it is the step that arms the scheduler.
Emits `topic.reset`. There is no bulk endpoint: the frontend fans out N calls,
matching bulk pause/resume/delete. Frontend:
`components/topics/ResetTopicCard` (an inline card, not a modal — this project
has no shadcn Dialog primitive), opened from a per-row `RotateCcw` button and
from the Topics page bulk action bar.
```

- [ ] **Step 4: Add the component to the CLAUDE.md frontend tree**

Under `components/topics/`:

```
│   │   ├── ResetTopicCard.tsx      Reset confirm + result card (row + bulk)
│   │   ├── TopicRow.tsx            One topic list row
│   │   └── BulkActionBar.tsx       Multi-select action bar
```

Also note in the frontend conventions section that `Topics.tsx` no longer
breaches the 250-line component limit, and update `techdebt/frontend/`
accordingly if a file there tracks it.

- [ ] **Step 5: Add the CHANGELOG entry**

Under `## [Unreleased]`, in an `### Added` subsection (create it if absent):

```markdown
- Reset a topic to re-download from scratch, per topic or over a multi-select.
  Removes the already-delivered torrents from the client (optionally deleting
  their files), clears the topic's check state and per-episode progress, and
  queues an immediate re-check. Settings, history and paused status are kept;
  torrents that could not be removed are reported as warnings rather than
  blocking the reset.
```

- [ ] **Step 6: Verify the whole thing builds and passes**

```
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l ./internal && go build ./... && go vet ./... && go test -race ./..."
```
```
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```
Expected: everything green, `gofmt -l` silent.

- [ ] **Step 7: Commit**

```bash
git add CLAUDE.md CHANGELOG.md
git commit -m "docs: document topic reset and the clientremove package"
```

---

## Manual acceptance

The repo tests use `pgxmock`, so they pin the SQL text but not Postgres's behaviour. Verify the semantics once by hand against the dev stack:

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d
```

Open http://localhost:34080 (`admin` / the `MARAUDER_ADMIN_INITIAL_PASSWORD` in `deploy/.env`) and, on a topic that has recorded deliveries:

1. Reset it with **delete data** ticked → the torrent disappears from the client, its files are gone, and the topic's delivery list empties.
2. Within the next scheduler tick the release is detected again and re-delivered, with a fresh delivery row.
3. Pause a second topic, reset it → it stays paused and does not re-check until resumed.
4. Check the topic's capability settings (quality / start season / start episode) survived the reset, and that its timeline shows a `topic.reset` entry.
5. Stop the client container, reset a third topic → the reset still succeeds and the result card lists a warning naming the client.

Confirm the JSONB key delete kept the rest of `extra`:

```sql
SELECT extra FROM topics WHERE id = '<topic-id>';
```

Expected: `quality` / `start_season` / `start_episode` present, `downloaded_episodes` absent.
