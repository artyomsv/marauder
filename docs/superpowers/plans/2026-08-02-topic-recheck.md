# Topic Recheck Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Check now" action that brings a topic's next scheduled check forward to now, so a topic sitting in error backoff can be retried without the destructive Reset.

**Architecture:** One narrow repository method writes `next_check_at = now()` (and nothing else), a handler exposes it at `POST /topics/{id}/recheck`, and the frontend adds a row action plus a bulk-bar entry that fans out one request per selected topic. The scheduler needs no changes — `DueForCheck` already selects `status IN ('active','error') AND next_check_at <= now()`.

**Tech Stack:** Go 1.25 (chi, pgx, uuid), React 19 + TypeScript (React Query, framer-motion, lucide-react), Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-08-02-topic-recheck-design.md`

## Global Constraints

- **Paused topics are excluded.** `DueForCheck` ignores `paused`, so writing `next_check_at` for one would silently do nothing. The SQL excludes them and the UI hides the action.
- **Write `next_check_at` and nothing else.** Not `status`, not `last_error`, not `last_checked_at`. The scheduler clears those on the next successful check; showing the old error until then is honest.
- **Ownership is enforced in the statement** (`AND user_id = $2`), matching `ResetCheckState` — not by caller discipline.
- **No audit entry, no `topic_events` row.** Matches `Pause`/`Resume`. `Reset` audits because it destroys state; this moves a timestamp the scheduler rewrites every tick.
- Go: tabs (gofmt), errors wrapped with `fmt.Errorf("...: %w", err)`, `errors.Is` for comparison.
- Frontend: 2-space indent, `interface` over `type` for object shapes, query keys from `lib/queryKeys.ts`, i18n keys in **both** `en` and `ru`.
- Backend verification (never install Go locally):
  ```bash
  docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
    sh -c "gofmt -l . && go build ./... && go vet ./... && go test -race ./..."
  ```
- Frontend verification (container-local `node_modules` volume — the Windows bind mount is too slow for vitest):
  ```bash
  docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
    sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
  ```

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/db/repo/topics.go` | Add `QueueRecheck` — the single UPDATE |
| `backend/internal/db/repo/topics_recheck_integration_test.go` *(new)* | Real-Postgres proof of the status/ownership guards, which pgxmock cannot give |
| `backend/internal/api/handlers/topics.go` | Add `QueueRecheck` to the `topicStore` interface + the `Recheck` handler |
| `backend/internal/api/handlers/topics_handler_test.go` | Add `QueueRecheck` to `fakeTopicStore` |
| `backend/internal/api/handlers/topics_recheck_test.go` *(new)* | Handler status-code matrix |
| `backend/internal/api/router.go` | Route registration |
| `frontend/src/pages/Topics.tsx` | `recheck` mutation + row/bulk wiring |
| `frontend/src/components/topics/TopicRow.tsx` | Row action, hidden when paused |
| `frontend/src/components/topics/BulkActionBar.tsx` | Bulk entry |
| `frontend/src/components/topics/TopicRow.test.tsx` *(new)* | Row action visibility + click |
| `frontend/src/i18n/en.ts`, `ru.ts` | Copy |
| `CLAUDE.md`, `CHANGELOG.md` | Structural note + release note |

---

### Task 1: Repository — `QueueRecheck`

**Files:**
- Modify: `backend/internal/db/repo/topics.go` (add after `ResetCheckState`)
- Test: `backend/internal/db/repo/topics_recheck_integration_test.go` *(new)*

**Interfaces:**
- Consumes: nothing.
- Produces: `func (r *Topics) QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error)` — `true` when a row was updated; `false` means not-found, not-owned, **or** paused (the handler distinguishes).

> The tests are integration-tagged because the whole behaviour is SQL semantics — the `status <> 'paused'` guard and the `user_id` scoping. The package's pgxmock tests pin query text without executing it, so they cannot tell you whether the WHERE clause actually excludes anything.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/db/repo/topics_recheck_integration_test.go`:

```go
//go:build integration

package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// TestIntegration_QueueRecheck_BringsAnErroredTopicForward is the feature's
// reason to exist: a topic parked on a six-hour backoff becomes due now.
func TestIntegration_QueueRecheck_BringsAnErroredTopicForward(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusError, nil)

	// Park it in the future, as a failing check would.
	if _, err := pool.Exec(ctx,
		`UPDATE topics SET next_check_at = now() + interval '6 hours' WHERE id = $1`, topic.ID); err != nil {
		t.Fatalf("park topic: %v", err)
	}

	topics := NewTopics(pool)
	ok, err := topics.QueueRecheck(ctx, topic.ID, userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if !ok {
		t.Fatal("QueueRecheck reported no row updated")
	}

	got := reload(t, pool, topic.ID)
	if d := time.Since(got.NextCheckAt); d > time.Minute || d < -time.Minute {
		t.Errorf("next_check_at is %s from now, want ~now", d)
	}
}

// TestIntegration_QueueRecheck_LeavesEverythingElseAlone pins the narrowness of
// the statement. Clearing status or last_error here would claim the topic is
// healthy before anything has verified that.
func TestIntegration_QueueRecheck_LeavesEverythingElseAlone(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusError, nil)

	if _, err := pool.Exec(ctx, `
UPDATE topics SET
    last_hash          = 'abc123',
    last_checked_at    = now() - interval '1 hour',
    consecutive_errors = 4,
    last_error         = 'boom',
    last_error_code    = 'auth'
WHERE id = $1`, topic.ID); err != nil {
		t.Fatalf("seed check state: %v", err)
	}
	before := reload(t, pool, topic.ID)

	if _, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, userID); err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}

	got := reload(t, pool, topic.ID)
	if got.Status != before.Status {
		t.Errorf("status = %q, want %q unchanged", got.Status, before.Status)
	}
	if got.LastError != before.LastError {
		t.Errorf("last_error = %q, want %q unchanged", got.LastError, before.LastError)
	}
	if got.LastErrorCode != before.LastErrorCode {
		t.Errorf("last_error_code = %q, want %q unchanged", got.LastErrorCode, before.LastErrorCode)
	}
	if got.LastHash != before.LastHash {
		t.Errorf("last_hash = %q, want %q unchanged", got.LastHash, before.LastHash)
	}
	if got.ConsecutiveErrors != before.ConsecutiveErrors {
		t.Errorf("consecutive_errors = %d, want %d unchanged", got.ConsecutiveErrors, before.ConsecutiveErrors)
	}
	// last_checked_at is half of the check-state token; leaving it alone keeps
	// "when was this last checked?" truthful and minimises token disruption.
	if (got.LastCheckedAt == nil) != (before.LastCheckedAt == nil) {
		t.Fatalf("last_checked_at nullability changed")
	}
	if got.LastCheckedAt != nil && !got.LastCheckedAt.Equal(*before.LastCheckedAt) {
		t.Errorf("last_checked_at = %v, want %v unchanged", got.LastCheckedAt, before.LastCheckedAt)
	}
}

// TestIntegration_QueueRecheck_IgnoresPausedTopics — DueForCheck skips paused
// rows, so moving next_check_at would be a silent no-op. Reporting false lets
// the handler answer 409 instead of a misleading 204.
func TestIntegration_QueueRecheck_IgnoresPausedTopics(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID := seedUser(t, pool)
	topic := seedTopic(t, pool, userID, domain.TopicStatusPaused, nil)
	before := reload(t, pool, topic.ID)

	ok, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if ok {
		t.Fatal("QueueRecheck reported an update for a paused topic")
	}

	got := reload(t, pool, topic.ID)
	if !got.NextCheckAt.Equal(before.NextCheckAt) {
		t.Errorf("next_check_at moved for a paused topic: %v -> %v",
			before.NextCheckAt, got.NextCheckAt)
	}
}

// TestIntegration_QueueRecheck_IsScopedToTheOwner keeps ownership in the
// statement rather than relying on the handler to check first.
func TestIntegration_QueueRecheck_IsScopedToTheOwner(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	owner := seedUser(t, pool)
	stranger := seedUser(t, pool)
	topic := seedTopic(t, pool, owner, domain.TopicStatusActive, nil)
	before := reload(t, pool, topic.ID)

	ok, err := NewTopics(pool).QueueRecheck(ctx, topic.ID, stranger)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if ok {
		t.Fatal("another user's topic was rechecked")
	}

	got := reload(t, pool, topic.ID)
	if !got.NextCheckAt.Equal(before.NextCheckAt) {
		t.Error("next_check_at moved for another user's topic")
	}
}

// TestIntegration_QueueRecheck_UnknownTopic reports false rather than erroring,
// so the handler can turn it into a 404.
func TestIntegration_QueueRecheck_UnknownTopic(t *testing.T) {
	pool := integrationPool(t)
	userID := seedUser(t, pool)
	ok, err := NewTopics(pool).QueueRecheck(context.Background(), uuid.New(), userID)
	if err != nil {
		t.Fatalf("QueueRecheck: %v", err)
	}
	if ok {
		t.Fatal("QueueRecheck reported an update for an unknown topic")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
docker network create marauder-itest-net 2>/dev/null
docker run --rm -d --name marauder-itest-pg --network marauder-itest-net \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=marauder_test postgres:17-alpine
docker run --rm --network marauder-itest-net -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend \
  -e MARAUDER_TEST_DB_URL="postgres://postgres:test@marauder-itest-pg:5432/marauder_test?sslmode=disable" \
  golang:1.25 sh -c "go test -tags=integration -race ./internal/db/repo/... -run QueueRecheck -v"
```

Expected: FAIL — `topics.QueueRecheck undefined`.

- [ ] **Step 3: Implement**

In `backend/internal/db/repo/topics.go`, immediately after `ResetCheckState`:

```go
// QueueRecheck brings a topic's next check forward to now, so the scheduler
// picks it up on its next tick. It is the non-destructive counterpart to
// ResetCheckState: a topic parked on a backoff after a tracker outage can be
// retried the moment the cause is fixed, without removing anything from the
// download client.
//
// It writes next_check_at and NOTHING else. status, last_error and
// last_error_code belong to the scheduler, which clears them on the next
// successful check — reporting the topic healthy before anything has verified
// that would be a lie. last_checked_at is left alone too: it has not checked
// anything yet, and it is half of the (last_checked_at, next_check_at)
// optimistic-concurrency token, so disturbing it needlessly would widen the
// window in which an in-flight check discards its own result.
//
// Paused topics are excluded because DueForCheck ignores them; moving their
// next_check_at would be a silent no-op that the API would have to report as
// success. Ownership is enforced by the statement, matching ResetCheckState.
//
// Returns whether a row was updated. False means the topic does not exist, is
// not this user's, or is paused — the handler tells those apart.
func (r *Topics) QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	const q = `
UPDATE topics SET
    next_check_at = now(),
    updated_at    = now()
WHERE id = $1 AND user_id = $2 AND status <> 'paused'`
	ct, err := r.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return false, fmt.Errorf("topics: queue recheck: %w", err)
	}
	return ct.RowsAffected() > 0, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Same command as Step 2. Expected: PASS (5 tests). Then tear down:

```bash
docker rm -f marauder-itest-pg; docker network rm marauder-itest-net
```

- [ ] **Step 5: Verify the normal suite is unaffected**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l . && go build ./... && go vet ./... && go test -race ./internal/db/repo/..."
```

Expected: no gofmt output, all pass.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/db/repo/topics.go backend/internal/db/repo/topics_recheck_integration_test.go
git commit -m "feat(repo): queue a topic for an immediate recheck"
```

---

### Task 2: Handler and route

**Files:**
- Modify: `backend/internal/api/handlers/topics.go` (the `topicStore` interface at line 32-40; new handler beside `setStatus`)
- Modify: `backend/internal/api/handlers/topics_handler_test.go` (extend `fakeTopicStore`)
- Modify: `backend/internal/api/router.go:187` (after the `reset` route)
- Test: `backend/internal/api/handlers/topics_recheck_test.go` *(new)*

**Interfaces:**
- Consumes: `QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error)` (Task 1).
- Produces: `func (h *Topics) Recheck(w http.ResponseWriter, r *http.Request)`; route `POST /api/v1/topics/{id}/recheck`; responses `204` / `409` / `404` / `400`.

- [ ] **Step 1: Extend the fake store**

In `backend/internal/api/handlers/topics_handler_test.go`, add to the `fakeTopicStore` struct (after the `resetErr` field):

```go
	// Captured QueueRecheck arguments.
	recheckCalls  [][2]uuid.UUID
	recheckOK     bool
	recheckErr    error
```

and add the method beside `ResetCheckState`:

```go
// QueueRecheck records (topicID, userID) per call so tests can assert the
// handler passed the right topic for the right owner.
func (s *fakeTopicStore) QueueRecheck(_ context.Context, id, userID uuid.UUID) (bool, error) {
	s.recheckCalls = append(s.recheckCalls, [2]uuid.UUID{id, userID})
	return s.recheckOK, s.recheckErr
}
```

- [ ] **Step 2: Write the failing tests**

Create `backend/internal/api/handlers/topics_recheck_test.go`:

```go
package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func TestRecheck_QueuesTheTopicAndReturns204(t *testing.T) {
	store := &fakeTopicStore{recheckOK: true}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	uid := uuid.New()
	id := uuid.New()
	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uid, nil), "id", id.String())
	h.Recheck(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body %s", w.Code, w.Body.String())
	}
	if len(store.recheckCalls) != 1 {
		t.Fatalf("QueueRecheck called %d times, want 1", len(store.recheckCalls))
	}
	if store.recheckCalls[0] != [2]uuid.UUID{id, uid} {
		t.Errorf("QueueRecheck got %v, want {topicID, userID} = {%s, %s}",
			store.recheckCalls[0], id, uid)
	}
}

// A paused topic must NOT be reported as success: the scheduler ignores paused
// rows, so a 204 would promise a check that never happens.
func TestRecheck_PausedTopic_Returns409(t *testing.T) {
	store := &fakeTopicStore{
		recheckOK: false,
		getByID:   &domain.Topic{ID: uuid.New(), Status: domain.TopicStatusPaused},
	}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body %s", w.Code, w.Body.String())
	}
}

// Not found and not-yours are the same answer, so the response cannot be used
// to probe for another user's topics.
func TestRecheck_UnknownTopic_Returns404(t *testing.T) {
	store := &fakeTopicStore{recheckOK: false, getByID: nil}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", w.Code, w.Body.String())
	}
}

func TestRecheck_MalformedID_Returns400(t *testing.T) {
	store := &fakeTopicStore{}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", "not-a-uuid")
	h.Recheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if len(store.recheckCalls) != 0 {
		t.Error("store must not be touched for a malformed id")
	}
}

func TestRecheck_StoreError_Returns500(t *testing.T) {
	store := &fakeTopicStore{recheckErr: errors.New("db down")}
	h := &Topics{Topics: store, BaseURL: "http://test"}

	w := httptest.NewRecorder()
	req := withURLParam(authedReq(t, uuid.New(), nil), "id", uuid.New().String())
	h.Recheck(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  go test ./internal/api/handlers/ -run TestRecheck -v
```

Expected: FAIL — `h.Recheck undefined`, and `*fakeTopicStore` does not implement `topicStore` until Step 4 adds the interface method.

- [ ] **Step 4: Implement**

Add to the `topicStore` interface in `backend/internal/api/handlers/topics.go` (after `ResetCheckState`):

```go
	QueueRecheck(ctx context.Context, id, userID uuid.UUID) (bool, error)
```

Add the handler immediately after `setStatus`:

```go
// Recheck handles POST /topics/{id}/recheck: bring the topic's next scheduled
// check forward to now.
//
// This exists because a failing topic backs off up to six hours, which is
// correct while a tracker is down and wrong the moment the operator fixes the
// cause. Reset is the only other way to force a check and it is destructive —
// it removes delivered torrents from the client.
//
// Nothing but next_check_at changes, so the stale error stays on screen until
// the check that follows actually says otherwise.
func (h *Topics) Recheck(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	id, ierr := uuid.Parse(chi.URLParam(r, "id"))
	if ierr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id"))
		return
	}
	queued, qerr := h.Topics.QueueRecheck(r.Context(), id, uid)
	if qerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal(qerr.Error()))
		return
	}
	if queued {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// No row matched: the topic is paused, or it is not this user's. Only the
	// rare failing path pays for the extra lookup, and it buys an honest
	// answer — a 404 for a paused topic would be a lie, and a 204 would
	// promise a check the scheduler is never going to run.
	t, gerr := h.Topics.GetByID(r.Context(), id, &uid)
	if gerr != nil || t == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("topic not found"))
		return
	}
	problem.Write(w, r, h.BaseURL, problem.ErrConflict("topic is paused; resume it first"))
}
```

Register the route in `backend/internal/api/router.go`, directly after the `reset` line:

```go
			r.Post("/topics/{id}/recheck", topicsH.Recheck)
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l . && go build ./... && go vet ./... && go test -race ./internal/api/..."
```

Expected: PASS (5 new tests plus the existing handler suite).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/handlers/topics.go backend/internal/api/handlers/topics_handler_test.go \
        backend/internal/api/handlers/topics_recheck_test.go backend/internal/api/router.go
git commit -m "feat(api): POST /topics/{id}/recheck to force an immediate check"
```

---

### Task 3: Frontend row action, bulk action and copy

**Files:**
- Modify: `frontend/src/components/topics/TopicRow.tsx` (`TopicRowActions` at line 26-31; action buttons at ~line 116-135)
- Modify: `frontend/src/components/topics/BulkActionBar.tsx`
- Modify: `frontend/src/pages/Topics.tsx` (mutations ~line 59-72; `BulkActionBar` props ~line 166; row `actions` ~line 208)
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`
- Test: `frontend/src/components/topics/TopicRow.test.tsx` *(new)*

**Interfaces:**
- Consumes: `POST /topics/{id}/recheck` (Task 2).
- Produces: `TopicRowActions.onRecheck: () => void`; `BulkActionBarProps.onRecheck: () => void`; i18n keys `topics.recheck`, `topics.bulk.recheck`.

- [ ] **Step 1: Add the copy**

`frontend/src/i18n/en.ts` — beside the existing `topics.bulk.reset` / `topics.reset.*` keys:

```ts
  "topics.recheck": "Check now",
  "topics.bulk.recheck": "Check now",
```

`frontend/src/i18n/ru.ts` — same keys:

```ts
  "topics.recheck": "Проверить сейчас",
  "topics.bulk.recheck": "Проверить сейчас",
```

- [ ] **Step 2: Write the failing test**

Create `frontend/src/components/topics/TopicRow.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TopicRow, type TopicRowActions, type TopicRowLookups } from "./TopicRow";
import type { Topic } from "@/lib/api";

const lookups: TopicRowLookups = {
  clientById: new Map(),
  defaultClient: null,
  notifierById: new Map(),
};

function makeTopic(status: string): Topic {
  return {
    ID: "t1",
    DisplayName: "Some Show",
    URL: "https://rutracker.org/forum/viewtopic.php?t=1",
    TrackerName: "rutracker",
    Status: status,
  } as unknown as Topic;
}

function makeActions(): TopicRowActions {
  return {
    onToggleSelect: vi.fn(),
    onEdit: vi.fn(),
    onRecheck: vi.fn(),
    onReset: vi.fn(),
    onDelete: vi.fn(),
  };
}

function renderRow(status: string, actions: TopicRowActions) {
  render(
    <TopicRow
      topic={makeTopic(status)}
      compact={false}
      selected={false}
      deletePending={false}
      lookups={lookups}
      actions={actions}
    />,
  );
}

describe("TopicRow check-now action", () => {
  it("calls onRecheck when clicked", async () => {
    const actions = makeActions();
    renderRow("error", actions);

    await userEvent.click(screen.getByRole("button", { name: /check now/i }));
    expect(actions.onRecheck).toHaveBeenCalledTimes(1);
  });

  it("is offered for an errored topic — the case the feature exists for", () => {
    renderRow("error", makeActions());
    expect(screen.getByRole("button", { name: /check now/i })).toBeInTheDocument();
  });

  // The scheduler skips paused topics entirely, so a Check now button on one
  // would silently do nothing. Hiding it is the honest option.
  it("is hidden for a paused topic", () => {
    renderRow("paused", makeActions());
    expect(screen.queryByRole("button", { name: /check now/i })).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx vitest run src/components/topics/TopicRow.test.tsx"
```

Expected: FAIL — `onRecheck` is not a property of `TopicRowActions`, and no such button renders.

- [ ] **Step 4: Implement the row action**

In `frontend/src/components/topics/TopicRow.tsx`:

Import the icon (line 2) and the translator:

```tsx
import { Pencil, RefreshCw, RotateCcw } from "lucide-react";
```

Add to `TopicRowActions`:

```tsx
export interface TopicRowActions {
  onToggleSelect: () => void;
  onEdit: () => void;
  onRecheck: () => void;
  onReset: () => void;
  onDelete: () => void;
}
```

Insert the button between Edit and Reset. `useT()` is already available in this file if it imports `useT` from `@/i18n`; if it does not, add `import { useT } from "@/i18n";` and `const t = useT();` at the top of the component:

```tsx
        {topic.Status !== "paused" && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("topics.recheck")}
            title={t("topics.recheck")}
            onClick={actions.onRecheck}
          >
            <RefreshCw className="size-4" />
          </Button>
        )}
```

- [ ] **Step 5: Run the test to verify it passes**

Same command as Step 3. Expected: PASS (3 tests).

- [ ] **Step 6: Add the bulk entry**

In `frontend/src/components/topics/BulkActionBar.tsx`, extend the icon import and props:

```tsx
import { Pause, Play, RefreshCw, RotateCcw, Trash2, Check, X } from "lucide-react";

export interface BulkActionBarProps {
  count: number;
  onPause: () => void;
  onResume: () => void;
  onRecheck: () => void;
  onReset: () => void;
  onDelete: () => void;
  onClear: () => void;
}
```

Destructure `onRecheck` alongside the others, and add the button immediately before the Reset button:

```tsx
        <Button variant="outline" size="sm" onClick={onRecheck}>
          <RefreshCw className="size-4" />
          {t("topics.bulk.recheck")}
        </Button>
```

- [ ] **Step 7: Wire the page**

In `frontend/src/pages/Topics.tsx`, add the mutation beside `pause`/`resume` (~line 70):

```tsx
  const recheck = useMutation({
    mutationFn: (id: string) => api.post<void>(`/topics/${id}/recheck`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.topics }),
  });
```

Add a bounded bulk helper next to it. The concurrency cap matters for the same reason bulk reset has one: an unbounded `Promise.all` over a large selection fires every request at once.

```tsx
  // Matches RESET_CONCURRENCY in ResetTopicCard — a bulk action should not
  // fire one request per selected topic simultaneously.
  const RECHECK_CONCURRENCY = 4;

  const bulkRecheck = async (ids: string[]) => {
    await mapWithConcurrency(ids, RECHECK_CONCURRENCY, (id) =>
      api.post<void>(`/topics/${id}/recheck`).catch(() => undefined),
    );
    await qc.invalidateQueries({ queryKey: QK.topics });
  };
```

with `import { mapWithConcurrency } from "@/lib/concurrency";` added to the imports.

> `.catch(() => undefined)` is deliberate: one paused or deleted topic in a large selection returning 409/404 must not abort the rest. The list refetch shows the true outcome.

Pass it to the bulk bar (~line 166):

```tsx
          onRecheck={() => void bulkRecheck([...selected])}
```

and to each row's actions (~line 208):

```tsx
                    onRecheck: () => recheck.mutate(t.ID),
```

- [ ] **Step 8: Run the full frontend check**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/host:ro" -v marauder-fe-nm:/app -w //app node:22-alpine \
  sh -c "cp -r /host/src /host/vitest.config.ts /host/vite.config.ts /host/index.html /host/tsconfig*.json /app/ 2>/dev/null; npx tsc --noEmit && npx vitest run"
```

Expected: typecheck clean, all tests pass. Any other component constructing `TopicRowActions` or `BulkActionBarProps` will fail typecheck until it supplies `onRecheck` — fix those call sites if the compiler names any.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/components/topics/TopicRow.tsx frontend/src/components/topics/TopicRow.test.tsx \
        frontend/src/components/topics/BulkActionBar.tsx frontend/src/pages/Topics.tsx \
        frontend/src/i18n/en.ts frontend/src/i18n/ru.ts
git commit -m "feat(topics): Check now action on a row and over a selection"
```

---

### Task 4: Documentation

**Files:**
- Modify: `CLAUDE.md` (the Topic-reset paragraph in the scheduler section)
- Modify: `CHANGELOG.md` (`[Unreleased]`)

- [ ] **Step 1: Update CLAUDE.md**

Immediately after the "**Topic reset:**" paragraph, add:

```markdown
**Topic recheck:** `POST /api/v1/topics/{id}/recheck` is the non-destructive
counterpart to reset — `Topics.QueueRecheck` writes `next_check_at = now()` and
nothing else, so a topic parked on a backoff (capped at 6h) after a tracker
outage can be retried the moment the cause is fixed. `status`, `last_error` and
`last_checked_at` are deliberately untouched: the scheduler clears them on the
next successful check, and reporting the topic healthy before anything verified
that would be a lie. Paused topics are excluded in the SQL because
`DueForCheck` ignores them, so the endpoint answers **409** rather than a 204
that promises a check which never runs (**404** for unknown/not-owned, which is
also what a paused topic belonging to someone else returns). Writing
`next_check_at` moves half of the `(last_checked_at, next_check_at)`
check-state token, so a recheck landing mid-check discards that worker's result
exactly as a reset does — bounded, self-correcting, and documented in the reset
design's "Known limitation". No audit row and no `topic_events` entry (it
matches pause/resume, not reset); the `check.*` events that follow are the
record. Frontend: a `RefreshCw` row action hidden for paused topics, plus a
bulk entry that fans out through `mapWithConcurrency`.
```

- [ ] **Step 2: Update CHANGELOG.md**

Under `## [Unreleased]`, add an `### Added` section (or extend an existing one):

```markdown
### Added

- **Check now** — retry a topic immediately instead of waiting out its backoff.
  A failing topic can back off for up to six hours, which is right while a
  tracker is down and wrong once you have fixed the cause; previously the only
  way to force a check was Reset, which removes the downloaded torrents from
  your client. Available per row and over a multi-select. Not offered for
  paused topics, since those are not checked at all.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md CHANGELOG.md
git commit -m "docs: record the topic recheck action"
```

---

### Task 5: Verify end to end, then open the PR

- [ ] **Step 1: Full backend + lint**

```bash
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 \
  sh -c "gofmt -l . && go build ./... && go vet ./... && go test -race ./..."
docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golangci/golangci-lint:v2.12.2 \
  golangci-lint run --timeout=5m
```

Expected: no gofmt output, all tests pass, `0 issues`.

- [ ] **Step 2: Run it against the live stack**

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d --no-deps --build backend frontend
docker restart deploy-gateway-1
```

`--no-deps` keeps postgres from being recreated; the gateway restart refreshes nginx's cached upstream IP.

Then, at http://localhost:34080:

1. Park a topic in the future to simulate a backoff:
   `docker exec deploy-db-1 psql -U marauder -d marauder -c "update topics set next_check_at = now() + interval '6 hours' where tracker_name='rutracker';"`
2. Click **Check now** on that row → the `TopicCheckStatus` chip shows "Checking…" within a tick, and `next_check_at` returns to a normal interval.
3. Pause a topic → confirm **Check now** is not offered on that row.
4. Select two topics → **Check now** in the bulk bar → both are queued.

- [ ] **Step 3: Push and open the PR**

```bash
git push -u origin feat/topic-recheck
gh pr create --base main \
  --title "feat(topics): add a Check now action to retry without resetting" \
  --body-file <path to a body file>
```

The title must start with `feat` — `.github/workflows/auto-release.yml` derives the semver bump from the PR title, and this is a user-visible addition (minor).

---

## Self-Review

**Spec coverage**

| Spec requirement | Task |
|---|---|
| `QueueRecheck` writes only `next_check_at`, ownership + paused in SQL | 1 |
| Returns bool; false covers not-found/not-owned/paused | 1 |
| `POST /topics/{id}/recheck`, 204/409/404 | 2 |
| Extra `Get` only on the failing path | 2 |
| No audit entry, no `topic_events` row | 2 (handler writes neither), 4 (documented) |
| Row action hidden when paused | 3 |
| Bulk entry via `mapWithConcurrency` | 3 |
| Invalidate `QK.topics` | 3 |
| No confirmation dialog, no bespoke progress UI | 3 (neither is added) |
| i18n in `en` **and** `ru` | 3 |
| Repo integration tests (5 listed cases) | 1 |
| Handler tests (204/409/404/400) | 2 — plus a 500 case for the store error |
| Frontend tests (visibility, click) | 3 |
| Check-state token interaction documented | 1 (doc comment), 4 (CLAUDE.md) |

**Placeholder scan:** none — every step carries runnable code or an exact command. The one bracketed item is the PR body path in Task 5 Step 3, which is genuinely author-supplied.

**Type consistency:** `QueueRecheck(ctx, id, userID) (bool, error)` is identical in the repo (Task 1), the `topicStore` interface, the fake, and the handler call (Task 2). `onRecheck` is the field name in `TopicRowActions`, `BulkActionBarProps`, the test's `makeActions`, and both wiring sites (Task 3). i18n keys `topics.recheck` / `topics.bulk.recheck` match between definition and use.
