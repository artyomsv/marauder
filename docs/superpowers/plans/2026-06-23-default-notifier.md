# Default Notifier + Notifier Editing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a per-type "default notifier" concept (issue #85): mark one notifier per type as default, route no-override topic notifications to the default set, add notifier editing, and show a `default` badge — consistent with the Clients UI.

**Architecture:** Add `notifiers.is_default` (migration 0008) with a partial unique index enforcing at-most-one-default per `(user, notifier_type)`. Setting a default transactionally auto-unsets the prior same-type default. The dispatcher's `SendVia(nil)` fallback changes from "all subscribed notifiers" to "the user's default notifiers only" (strict). Notifier editing gains `GET /notifiers/{id}` + `PUT /notifiers/{id}`, mirroring Clients. Frontend adds an Edit dialog, a default toggle/badge, and an empty-state hint.

**Tech Stack:** Go 1.25 (pgx v5, pgxmock v3, goose migrations, chi, zerolog), React 19 + TypeScript + Vite + Tailwind + shadcn/ui + React Query.

## Global Constraints

- **Per-type default, not global.** `is_default` is unique per `(user_id, notifier_name)` — a user may have a default Telegram AND a default email simultaneously, but only one default per type. Enforced by a DB partial unique index `WHERE is_default`.
- **Auto-unset same-type.** Marking a notifier default (on create OR update) transactionally clears `is_default` on the user's other notifiers **of the same `notifier_name`**. Different types are untouched.
- **Strict fallback routing.** A topic with `notifier_id = nil` routes to the user's **default notifiers only** (filtered by event subscription). If the user has **no** defaults, it sends **nothing** (accepted by the issue owner). This is a behavior change from today's "all subscribed notifiers" fan-out and MUST be noted in the CHANGELOG.
- **Session-expiry stays global.** `Dispatcher.Send` (used by the scheduler's credential session-expiry "error" alert) is unchanged — it still fans out to all subscribed notifiers. Only the `SendVia(nil)` topic-fallback branch changes.
- **Edit scope = full.** `PUT /notifiers/{id}` edits display name, events, config, and `is_default`, re-running the plugin `Test()` like Create. Mirror `EditClientCard` exactly (it re-sends full config, hydrated from a `GET /notifiers/{id}` that returns the decrypted config).
- **Badge parity.** The default badge is `<Badge variant="success">default</Badge>`, matching `Clients.tsx:127` verbatim.
- **Migration is additive & reversible.** New column `NOT NULL DEFAULT false`; existing rows become non-default. Down-migration drops index then column.
- **Backend verify (Docker, never local Go):** `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "<cmd>"`. CI also runs `gofmt` via golangci-lint — run `gofmt -l .` and fix before committing.
- **Frontend verify:** `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "<cmd>"`.
- **Conventions:** `interface` (not `type`) for object shapes; React Query keys from `QK`; manual fakes not mock frameworks; Go errors wrapped with `%w`. Commits: imperative ≤72 chars, reference `#85`, NO AI/Claude attribution, NO `Co-Authored-By`.

---

## File Structure

**Backend**
- Create: `backend/internal/db/migrations/0008_add_notifier_is_default.sql` — column + partial unique index.
- Modify: `backend/internal/domain/domain.go` — add `Notifier.IsDefault bool`.
- Modify: `backend/internal/db/repo/notifiers.go` — `notifiersPool` seam; scan `is_default`; `Create` takes `is_default` + auto-unset; new `Update` (transactional, auto-unset).
- Create: `backend/internal/db/repo/notifiers_test.go` — pgxmock tests for create/update/auto-unset/scan.
- Modify: `backend/internal/api/handlers/notifiers.go` — `notifierStore` seam; `notifierView.is_default`; `Create` accepts `is_default`; new `Get` + `Update` handlers.
- Create: `backend/internal/api/handlers/notifiers_handler_test.go` — handler tests with a fake store.
- Modify: `backend/internal/api/router.go` — register `GET` + `PUT /notifiers/{id}`.
- Modify: `backend/internal/notify/dispatcher.go` — `SendVia(nil)` → default-set fan-out.
- Modify: `backend/internal/notify/dispatcher_test.go` — default-set fan-out tests.

**Frontend**
- Modify: `frontend/src/lib/api.ts` — `getNotifier`, `updateNotifier`; `NotifierDetail` type.
- Modify: `frontend/src/lib/queryKeys.ts` — `QK.notifier(id)`.
- Modify: `frontend/src/pages/Notifiers.tsx` — `is_default` on `NotifierView`; default badge; events + is_default in Add form; Edit button; empty-state "no default" hint.
- Create: `frontend/src/components/notifiers/EditNotifierCard.tsx` — edit dialog (mirror `EditClientCard`).

**Docs**
- Modify: `CHANGELOG.md` (`[Unreleased]`), `CLAUDE.md` (notify/domain/db notes).

---

## Task 1: Backend persistence — migration, domain, repo, repo tests

**Files:**
- Create: `backend/internal/db/migrations/0008_add_notifier_is_default.sql`
- Modify: `backend/internal/domain/domain.go:152-162`
- Modify: `backend/internal/db/repo/notifiers.go`
- Create: `backend/internal/db/repo/notifiers_test.go`

**Interfaces:**
- Produces:
  - `domain.Notifier.IsDefault bool`
  - `(*repo.Notifiers).Create(ctx, *domain.Notifier) (*domain.Notifier, error)` — now honours `n.IsDefault` (auto-unsets same-type)
  - `(*repo.Notifiers).Update(ctx, id, userID uuid.UUID, notifierName, displayName string, events []string, isDefault bool, configEnc, configNonce []byte) error` — transactional, auto-unsets same-type when `isDefault`, returns `ErrNotFound` when the row is absent
  - `GetByID`/`ListForUser` now scan `is_default`

- [ ] **Step 1: Write the migration**

Create `backend/internal/db/migrations/0008_add_notifier_is_default.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE notifiers ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX uq_notifiers_default_per_type
    ON notifiers (user_id, notifier_name) WHERE is_default;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uq_notifiers_default_per_type;
ALTER TABLE notifiers DROP COLUMN is_default;
-- +goose StatementEnd
```

- [ ] **Step 2: Add the domain field**

In `backend/internal/domain/domain.go`, add `IsDefault` to `Notifier` (after `Events`):

```go
type Notifier struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	NotifierName string
	DisplayName  string
	ConfigEnc    []byte
	ConfigNonce  []byte
	Events       []string
	IsDefault    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

- [ ] **Step 3: Write the failing repo tests**

Create `backend/internal/db/repo/notifiers_test.go`. (Mirrors the pgxmock harness in `topics_test.go`; the repo will be given a `notifiersPool` seam in Step 5 so `pgxmock.NewPool()` can be injected.)

```go
package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

func newMockNotifiers(t *testing.T) (*Notifiers, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &Notifiers{pool: mock}, mock
}

func TestNotifiers_Create_NonDefault_NoUnset(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	now := time.Now().UTC()
	mock.ExpectQuery(`INSERT INTO notifiers`).
		WithArgs(uid, "telegram", "T", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), false).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uuid.New(), now, now))

	_, err := r.Create(context.Background(), &domain.Notifier{
		UserID: uid, NotifierName: "telegram", DisplayName: "T",
		ConfigEnc: []byte("e"), ConfigNonce: []byte("n"), Events: []string{"updated"}, IsDefault: false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestNotifiers_Create_Default_UnsetsSameType(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	now := time.Now().UTC()
	mock.ExpectBegin()
	// unset same-type defaults first
	mock.ExpectExec(`UPDATE notifiers SET is_default = false`).
		WithArgs(uid, "telegram").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery(`INSERT INTO notifiers`).
		WithArgs(uid, "telegram", "T", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), true).
		WillReturnRows(pgxmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uuid.New(), now, now))
	mock.ExpectCommit()

	_, err := r.Create(context.Background(), &domain.Notifier{
		UserID: uid, NotifierName: "telegram", DisplayName: "T",
		ConfigEnc: []byte("e"), ConfigNonce: []byte("n"), Events: []string{"updated"}, IsDefault: true,
	})
	if err != nil {
		t.Fatalf("Create default: %v", err)
	}
}

func TestNotifiers_Update_Default_UnsetsSameTypeThenUpdates(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	id := uuid.New()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE notifiers SET is_default = false`).
		WithArgs(uid, "telegram", id).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE notifiers SET`).
		WithArgs(id, uid, "T2", pgxmock.AnyArg(), true, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err := r.Update(context.Background(), id, uid, "telegram", "T2", []string{"updated"}, true, []byte("e"), []byte("n"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestNotifiers_Update_NotFound(t *testing.T) {
	r, mock := newMockNotifiers(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet: %v", err)
		}
	})
	uid := uuid.New()
	id := uuid.New()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE notifiers SET`).
		WithArgs(id, uid, "T", pgxmock.AnyArg(), false, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := r.Update(context.Background(), id, uid, "telegram", "T", []string{"updated"}, false, []byte("e"), []byte("n"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/db/repo/..."`
Expected: FAIL — `Notifiers` has no `pool` field of the seam type, no `Update`, and `Create` doesn't branch on `IsDefault`; the package won't compile.

- [ ] **Step 5: Implement the repo**

Replace `backend/internal/db/repo/notifiers.go` with:

```go
package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/artyomsv/marauder/backend/internal/domain"
)

// notifiersPool is the minimal subset of *pgxpool.Pool used by Notifiers.
// Defined as an unexported interface so tests can substitute pgxmock.
type notifiersPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Notifiers repository.
type Notifiers struct {
	pool notifiersPool
}

// NewNotifiers constructs the repository.
func NewNotifiers(pool *pgxpool.Pool) *Notifiers {
	return &Notifiers{pool: pool}
}

const notifierColumns = `id, user_id, notifier_name, display_name, config_enc, config_nonce,
       events, is_default, created_at, updated_at`

// unsetDefaultForType clears is_default on the user's notifiers of the given
// type, optionally excluding one id (uuid.Nil = exclude nothing). Runs inside
// the caller's transaction so the "exactly one default per type" invariant
// holds atomically.
func unsetDefaultForType(ctx context.Context, tx pgx.Tx, userID uuid.UUID, notifierName string, exceptID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE notifiers SET is_default = false, updated_at = now()
         WHERE user_id = $1 AND notifier_name = $2 AND id <> $3 AND is_default`,
		userID, notifierName, exceptID)
	return err
}

// Create inserts a new notifier config. When n.IsDefault is true it first
// clears the same-type default in a transaction.
func (r *Notifiers) Create(ctx context.Context, n *domain.Notifier) (*domain.Notifier, error) {
	const ins = `
INSERT INTO notifiers (user_id, notifier_name, display_name, config_enc, config_nonce, events, is_default)
VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING id, created_at, updated_at`
	if !n.IsDefault {
		err := r.pool.QueryRow(ctx, ins,
			n.UserID, n.NotifierName, n.DisplayName, n.ConfigEnc, n.ConfigNonce, n.Events, n.IsDefault,
		).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
		return n, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE notifiers SET is_default = false, updated_at = now()
         WHERE user_id = $1 AND notifier_name = $2 AND is_default`,
		n.UserID, n.NotifierName); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, ins,
		n.UserID, n.NotifierName, n.DisplayName, n.ConfigEnc, n.ConfigNonce, n.Events, n.IsDefault,
	).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// GetByID fetches a notifier by id, scoped to user.
func (r *Notifiers) GetByID(ctx context.Context, id uuid.UUID, userID uuid.UUID) (*domain.Notifier, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+notifierColumns+` FROM notifiers WHERE id = $1 AND user_id = $2`, id, userID)
	var n domain.Notifier
	err := row.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
		&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.IsDefault, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

// ListForUser returns all notifiers for a user.
func (r *Notifiers) ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+notifierColumns+` FROM notifiers WHERE user_id = $1 ORDER BY display_name ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Notifier
	for rows.Next() {
		var n domain.Notifier
		if err := rows.Scan(&n.ID, &n.UserID, &n.NotifierName, &n.DisplayName,
			&n.ConfigEnc, &n.ConfigNonce, &n.Events, &n.IsDefault, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// Update overwrites the mutable fields of a notifier. When isDefault is true it
// transactionally clears the same-type default first. Returns ErrNotFound when
// the row is absent. notifierName is the notifier's immutable type (passed by
// the handler from the existing row) so the same-type unset targets the right rows.
func (r *Notifiers) Update(ctx context.Context, id, userID uuid.UUID, notifierName, displayName string,
	events []string, isDefault bool, configEnc, configNonce []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if isDefault {
		if err := unsetDefaultForType(ctx, tx, userID, notifierName, id); err != nil {
			return err
		}
	}
	ct, err := tx.Exec(ctx,
		`UPDATE notifiers SET display_name = $3, events = $4, is_default = $5,
            config_enc = $6, config_nonce = $7, updated_at = now()
         WHERE id = $1 AND user_id = $2`,
		id, userID, displayName, events, isDefault, configEnc, configNonce)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// Delete removes a notifier.
func (r *Notifiers) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM notifiers WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

> Note: `unsetDefaultForType` with `exceptID = id` produces the `id <> $3` form the Update test expects; Create's inline unset (no exclusion) uses the 2-arg form the Create test expects. Keep both SQL strings exactly as written so the pgxmock regexes match.

- [ ] **Step 6: Run repo tests + gofmt**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "gofmt -l ./internal/db/repo && go test ./internal/db/repo/..."`
Expected: `gofmt -l` prints nothing; repo tests PASS. (The handler package won't compile yet — its `Notifiers` handler still references the old repo `Create` signature shape; that's Task 2. Scope this run to `./internal/db/repo/...`.)

- [ ] **Step 7: Commit**

```bash
git add backend/internal/db/migrations/0008_add_notifier_is_default.sql backend/internal/domain/domain.go backend/internal/db/repo/notifiers.go backend/internal/db/repo/notifiers_test.go
git commit -m "feat: persist per-type default notifier (#85)"
```

---

## Task 2: Notifier handler — Get, Update, Create default, view

**Files:**
- Modify: `backend/internal/api/handlers/notifiers.go`
- Create: `backend/internal/api/handlers/notifiers_handler_test.go`
- Modify: `backend/internal/api/router.go:145-148`

**Interfaces:**
- Consumes: `(*repo.Notifiers).Update(...)`, `Create`, `GetByID` (Task 1).
- Produces:
  - `GET /api/v1/notifiers/{id}` → `{id, notifier_name, display_name, events, is_default, config, created_at, updated_at}` (decrypted `config`)
  - `PUT /api/v1/notifiers/{id}` accepting `{display_name, events, is_default, config}`
  - `notifierView` gains `"is_default"`; `createNotifierReq` gains `"is_default"`

- [ ] **Step 1: Write the failing handler tests**

Create `backend/internal/api/handlers/notifiers_handler_test.go`. Introduce a `notifierStore` seam (added to the handler in Step 3) so a fake can be injected. Reuse the package's existing shared helpers `authedReq(t, uid, body)` and `withURLParam(req, "id", idStr)` (defined in `topics_handler_test.go`, same `handlers` package — do NOT redefine them). Register an offline fake notifier plugin so `plugin.Test()` does no network I/O.

```go
package handlers

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/db/repo"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// hnotifier is an offline test notifier plugin: Test/Send never touch the
// network, so handler tests for Create/Update (which call plugin.Test) are
// deterministic. Registered once under a unique name.
type hnotifier struct{}

func (hnotifier) Name() string                                  { return "test-handler-notifier" }
func (hnotifier) DisplayName() string                           { return "Test Handler Notifier" }
func (hnotifier) ConfigSchema() map[string]any                  { return nil }
func (hnotifier) Test(context.Context, []byte) error            { return nil }
func (hnotifier) Send(context.Context, []byte, domain.Message) error { return nil }

func init() { registry.RegisterNotifier(hnotifier{}) }

func newNotifierTestMaster(t *testing.T) *crypto.MasterKey {
	t.Helper()
	mk, err := crypto.LoadMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	return mk
}

type fakeNotifierStore struct {
	got          *domain.Notifier
	updateCalled bool
	updateIsDef  bool
	getByID      *domain.Notifier
}

func (s *fakeNotifierStore) ListForUser(context.Context, uuid.UUID) ([]*domain.Notifier, error) {
	return nil, nil
}
func (s *fakeNotifierStore) Create(_ context.Context, n *domain.Notifier) (*domain.Notifier, error) {
	s.got = n
	n.ID = uuid.New()
	return n, nil
}
func (s *fakeNotifierStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Notifier, error) {
	if s.getByID == nil {
		return nil, repo.ErrNotFound
	}
	return s.getByID, nil
}
func (s *fakeNotifierStore) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *fakeNotifierStore) Update(_ context.Context, _, _ uuid.UUID, _, _ string, _ []string, isDefault bool, _, _ []byte) error {
	s.updateCalled = true
	s.updateIsDef = isDefault
	return nil
}

func TestNotifiers_Create_PassesIsDefault(t *testing.T) {
	store := &fakeNotifierStore{}
	h := &Notifiers{Notifiers: store, Master: newNotifierTestMaster(t), BaseURL: "http://x"}

	body := `{"notifier_name":"test-handler-notifier","display_name":"W","is_default":true,"config":{"k":"v"}}`
	rr := httptest.NewRecorder()
	h.Create(rr, authedReq(t, uuid.New(), body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if store.got == nil || !store.got.IsDefault {
		t.Errorf("Create did not forward is_default=true")
	}
}

func TestNotifiers_Update_PassesIsDefault(t *testing.T) {
	id := uuid.New()
	store := &fakeNotifierStore{getByID: &domain.Notifier{ID: id, NotifierName: "test-handler-notifier"}}
	h := &Notifiers{Notifiers: store, Master: newNotifierTestMaster(t), BaseURL: "http://x"}

	body := `{"display_name":"W2","events":["updated"],"is_default":true,"config":{"k":"v"}}`
	rr := httptest.NewRecorder()
	h.Update(rr, withURLParam(authedReq(t, uuid.New(), body), "id", id.String()))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !store.updateCalled || !store.updateIsDef {
		t.Errorf("Update not called with is_default=true (called=%v def=%v)", store.updateCalled, store.updateIsDef)
	}
}
```

> `authedReq` accepts the body as a string OR a struct (it JSON-encodes structs) — the calls above pass raw JSON strings. If a shared MasterKey test helper already exists in the `handlers` test package, use it instead of `newNotifierTestMaster`. `registry.RegisterNotifier` panics on duplicate names, so keep the `test-handler-notifier` name unique within the package's test binary.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/..."`
Expected: FAIL — `Notifiers` handler has no `Update`/`Get`, no `notifierStore` seam, `createNotifierReq` has no `is_default`.

- [ ] **Step 3: Implement the handler changes**

In `backend/internal/api/handlers/notifiers.go`:

(a) Add the consumer seam and switch the struct field to it:

```go
// notifierStore is the consumer seam over *repo.Notifiers.
type notifierStore interface {
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error)
	Create(ctx context.Context, n *domain.Notifier) (*domain.Notifier, error)
	GetByID(ctx context.Context, id, userID uuid.UUID) (*domain.Notifier, error)
	Delete(ctx context.Context, id, userID uuid.UUID) error
	Update(ctx context.Context, id, userID uuid.UUID, notifierName, displayName string, events []string, isDefault bool, configEnc, configNonce []byte) error
}

// Notifiers handles /notifiers.
type Notifiers struct {
	Notifiers notifierStore
	Master    *crypto.MasterKey
	BaseURL   string
}
```

(`*repo.Notifiers` satisfies `notifierStore`, so router wiring is unchanged.)

(b) Add `IsDefault` to `notifierView` and `notifierToView`:

```go
type notifierView struct {
	ID           string   `json:"id"`
	NotifierName string   `json:"notifier_name"`
	DisplayName  string   `json:"display_name"`
	Events       []string `json:"events"`
	IsDefault    bool     `json:"is_default"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}
```
Set `IsDefault: n.IsDefault` in `notifierToView`.

(c) Add `IsDefault` to `createNotifierReq` and pass it through in `Create`:

```go
type createNotifierReq struct {
	NotifierName string          `json:"notifier_name"`
	DisplayName  string          `json:"display_name"`
	Events       []string        `json:"events"`
	IsDefault    bool            `json:"is_default"`
	Config       json.RawMessage `json:"config"`
}
```
In `Create`, add `IsDefault: req.IsDefault,` to the `&domain.Notifier{...}` literal.

(d) Add `Get` (returns decrypted config — mirrors clients) and `Update`:

```go
// Get handles GET /notifiers/{id}. Returns the notifier with its decrypted
// config so the edit form can hydrate. Config secrets never leave the user's
// own authenticated session.
func (h *Notifiers) Get(w http.ResponseWriter, r *http.Request) {
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
	n, err := h.Notifiers.GetByID(r.Context(), id, uid)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("notifier not found"))
		return
	}
	raw, derr := h.Master.Decrypt(n.ConfigEnc, n.ConfigNonce)
	if derr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt: "+derr.Error()))
		return
	}
	v := notifierToView(n)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": v.ID, "notifier_name": v.NotifierName, "display_name": v.DisplayName,
		"events": v.Events, "is_default": v.IsDefault, "config": json.RawMessage(raw),
		"created_at": v.CreatedAt, "updated_at": v.UpdatedAt,
	})
}

type updateNotifierReq struct {
	DisplayName string          `json:"display_name"`
	Events      []string        `json:"events"`
	IsDefault   bool            `json:"is_default"`
	Config      json.RawMessage `json:"config"`
}

// Update handles PUT /notifiers/{id}. Re-runs the plugin Test() like Create.
// The notifier type is immutable — taken from the stored row, not the request.
func (h *Notifiers) Update(w http.ResponseWriter, r *http.Request) {
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
	var req updateNotifierReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.DisplayName == "" || len(req.Config) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("display_name and config are required"))
		return
	}
	existing, gerr := h.Notifiers.GetByID(r.Context(), id, uid)
	if gerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("notifier not found"))
		return
	}
	plugin := registry.GetNotifier(existing.NotifierName)
	if plugin == nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("notifier plugin not installed"))
		return
	}
	if err := plugin.Test(r.Context(), req.Config); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("notifier test failed: "+err.Error()))
		return
	}
	enc, nonce, eerr := h.Master.Encrypt(req.Config)
	if eerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt: "+eerr.Error()))
		return
	}
	events := req.Events
	if len(events) == 0 {
		events = []string{"updated", "error"}
	}
	if err := h.Notifiers.Update(r.Context(), id, uid, existing.NotifierName, req.DisplayName, events, req.IsDefault, enc, nonce); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			problem.Write(w, r, h.BaseURL, problem.ErrNotFound("notifier not found"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("update notifier: "+err.Error()))
		return
	}
	updated, _ := h.Notifiers.GetByID(r.Context(), id, uid)
	writeJSON(w, http.StatusOK, notifierToView(updated))
}
```

Add `"context"` to the imports if not already present (the seam method signatures use it).

- [ ] **Step 4: Register the routes**

In `backend/internal/api/router.go`, after the existing notifier routes (line ~148) add:

```go
			r.Get("/notifiers/{id}", notifiersH.Get)
			r.Put("/notifiers/{id}", notifiersH.Update)
```

- [ ] **Step 5: Run handler tests + full backend build + gofmt**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "gofmt -l . && go build ./... && go vet ./... && go test ./internal/api/... ./internal/db/repo/..."`
Expected: gofmt clean; build OK; tests PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/handlers/notifiers.go backend/internal/api/handlers/notifiers_handler_test.go backend/internal/api/router.go
git commit -m "feat: add notifier edit + get endpoints with default flag (#85)"
```

---

## Task 3: Dispatcher — default-set fallback routing

**Files:**
- Modify: `backend/internal/notify/dispatcher.go:61-81`
- Modify: `backend/internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `domain.Notifier.IsDefault` (Task 1).
- Produces: `SendVia(ctx, userID, nil, event, msg)` now fans out to the user's **default** notifiers only (subscription-filtered), returning the success count. `Send` is unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/notify/dispatcher_test.go`:

```go
func TestSendVia_NilID_SendsToDefaultsOnly(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)
	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), IsDefault: true, ConfigEnc: enc, ConfigNonce: nonce},
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), IsDefault: false, ConfigEnc: enc, ConfigNonce: nonce},
	}
	got := d.SendVia(context.Background(), uid, nil, "updated", domain.Message{Title: "t"})
	if got != 1 {
		t.Errorf("want 1 (only the default notifier), got %d", got)
	}
}

func TestSendVia_NilID_NoDefaults_SendsNothing(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)
	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), IsDefault: false, ConfigEnc: enc, ConfigNonce: nonce},
	}
	got := d.SendVia(context.Background(), uid, nil, "updated", domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0 (no defaults set = strict silence), got %d", got)
	}
}

func TestSendVia_NilID_DefaultRespectsSubscription(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{}
	d, enc, nonce := newTestDispatcher(t, repo)
	repo.items = []*domain.Notifier{
		{ID: uuid.New(), UserID: uid, NotifierName: okPlugin.Name(), IsDefault: true, Events: []string{"error"}, ConfigEnc: enc, ConfigNonce: nonce},
	}
	got := d.SendVia(context.Background(), uid, nil, "updated", domain.Message{Title: "t"})
	if got != 0 {
		t.Errorf("want 0 (default not subscribed to updated), got %d", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/..."`
Expected: FAIL — `SendVia(nil)` currently delegates to `Send` (all subscribed), so test 1 gets 2 and test 2 gets 1.

- [ ] **Step 3: Implement the fallback change**

In `backend/internal/notify/dispatcher.go`, replace the `notifierID == nil` branch of `SendVia` (lines 62-64) and update the doc comment:

```go
// SendVia delivers msg through a single notifier (the one whose ID matches
// notifierID) when notifierID is non-nil. When notifierID is nil it fans out
// to the user's DEFAULT notifiers only (subscription-filtered) — a topic with
// no per-topic override routes to the defaults; if the user has marked no
// defaults, nothing is sent (strict). Returns the count of successes.
func (d *Dispatcher) SendVia(ctx context.Context, userID uuid.UUID, notifierID *uuid.UUID, event string, msg domain.Message) int {
	list, err := d.notifiers.ListForUser(ctx, userID)
	if err != nil {
		d.log.Warn().Err(err).Str("user_id", userID.String()).Msg("notify: list notifiers failed")
		return 0
	}
	if notifierID == nil {
		sent := 0
		for _, n := range list {
			if n.IsDefault && d.sendOne(ctx, n, event, msg) {
				sent++
			}
		}
		return sent
	}
	for _, n := range list {
		if n.ID != *notifierID {
			continue
		}
		if d.sendOne(ctx, n, event, msg) {
			return 1
		}
		return 0
	}
	d.log.Warn().Str("notifier_id", notifierID.String()).Msg("notify: per-topic notifier not found")
	return 0
}
```

(`Send` stays as-is and is still used by the scheduler's session-expiry alert.)

- [ ] **Step 4: Run to verify they pass**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/notify/... ./internal/scheduler/..."`
Expected: PASS — including existing scheduler tests (they assert `notifyUpdated` calls `SendVia` with the topic's `NotifierID`; behavior of the fake is unchanged).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/notify/dispatcher.go backend/internal/notify/dispatcher_test.go
git commit -m "feat: route no-override topic alerts to default notifiers (#85)"
```

---

## Task 4: Frontend API client + query key

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/lib/queryKeys.ts`

**Interfaces:**
- Produces:
  - `api.getNotifier(id) → NotifierDetail`
  - `api.updateNotifier(id, body) → NotifierView`
  - `NotifierDetail` interface (`config` + `is_default`)
  - `QK.notifier(id)`

- [ ] **Step 1: Add the query key**

In `frontend/src/lib/queryKeys.ts`, add inside `QK` (next to `notifiers`):

```ts
  notifier: (id: string) => ["notifier", id] as const,
```

- [ ] **Step 2: Add the types + methods to api.ts**

In `frontend/src/lib/api.ts`, add the request/response interfaces near the other notifier types and two methods on the `api` object (mirror the existing `updateTopic`/client patterns):

```ts
// GET /notifiers/{id} — includes decrypted config for the edit form.
export interface NotifierDetail {
  id: string;
  notifier_name: string;
  display_name: string;
  events: string[];
  is_default: boolean;
  config: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

// PUT /notifiers/{id} body.
export interface UpdateNotifierBody {
  display_name: string;
  events: string[];
  is_default: boolean;
  config: Record<string, string>;
}
```

Add to the `api` object:

```ts
  getNotifier: (id: string) => request<NotifierDetail>("GET", `/notifiers/${id}`),
  updateNotifier: (id: string, body: UpdateNotifierBody) =>
    request<{ id: string }>("PUT", `/notifiers/${id}`, body),
```

> Match the existing call style in `api.ts` — if the file uses `api.get`/`api.put` helpers rather than a raw `request(...)`, use those instead (read the surrounding methods first and follow whichever form is already there).

- [ ] **Step 3: Verify typecheck**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck"`
Expected: PASS (no consumers yet; this is type-only scaffolding for Task 5/6).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/lib/queryKeys.ts
git commit -m "feat: add notifier get/update API client (#85)"
```

---

## Task 5: Frontend Notifiers page — badge, default toggle, events, empty-state

**Files:**
- Modify: `frontend/src/pages/Notifiers.tsx`

**Interfaces:**
- Consumes: nothing new from Task 4 yet (the Edit dialog is Task 6).
- Produces: `NotifierView.is_default`; the default `Badge`; `is_default` + events in the Add form; the no-default hint.

- [ ] **Step 1: Add `is_default` to the page's `NotifierView` type**

In `frontend/src/pages/Notifiers.tsx`, extend the `NotifierView` type (line 18):

```ts
type NotifierView = {
  id: string;
  notifier_name: string;
  display_name: string;
  events: string[];
  is_default: boolean;
  created_at: string;
  updated_at: string;
};
```

- [ ] **Step 2: Render the default badge**

In the card `badges={...}` block, add the badge right after the `notifier_name` badge (mirror `Clients.tsx:127` exactly):

```tsx
                  <Badge variant="outline" className="font-mono">
                    {n.notifier_name}
                  </Badge>
                  {n.is_default && <Badge variant="success">default</Badge>}
```

- [ ] **Step 3: Add the no-default empty-state hint**

Directly above the `<div className="grid gap-4 md:grid-cols-2">` that maps the items (around line 116), add a warning shown only when notifiers exist but none is default:

```tsx
      {items.length > 0 && !items.some((n) => n.is_default) && (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          No default notifier set. Topics created without an explicit notifier
          won't notify anyone until you mark at least one notifier as default.
        </div>
      )}
```

- [ ] **Step 4: Add events + is_default to the Add form**

In `AddNotifierCard`, add state and inputs. After `const [config, setConfig] = ...` add:

```tsx
  const [events, setEvents] = useState<string[]>(["updated", "error"]);
  const [isDefault, setIsDefault] = useState(false);
```

Send them in the create mutation body:

```tsx
      api.post("/notifiers", {
        notifier_name: pluginName,
        display_name: displayName,
        events,
        is_default: isDefault,
        config,
      }),
```

Add the controls just before the submit-button row (after the config `fields` grid):

```tsx
          <div className="flex flex-wrap items-center gap-4 text-sm">
            <span className="text-muted-foreground">Notify on:</span>
            {(["updated", "error"] as const).map((ev) => (
              <label key={ev} className="inline-flex items-center gap-1.5">
                <input
                  type="checkbox"
                  checked={events.includes(ev)}
                  onChange={(e) =>
                    setEvents((prev) =>
                      e.target.checked ? [...prev, ev] : prev.filter((x) => x !== ev),
                    )
                  }
                />
                {ev === "updated" ? "new releases" : "errors"}
              </label>
            ))}
          </div>
          <label className="inline-flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={isDefault}
              onChange={(e) => setIsDefault(e.target.checked)}
            />
            Use as a default notifier (one per type) for topics without an explicit notifier
          </label>
```

- [ ] **Step 5: Add an Edit button to each card**

The Edit dialog component itself is Task 6; here add the page wiring. At the top of `NotifiersPage` add state:

```tsx
  const [editingId, setEditingId] = useState<string | null>(null);
```

Import the (Task 6) component and `Pencil` icon at the top of the file:

```tsx
import { Plus, Loader2, CheckCircle2, Bell, AlertCircle, Pencil } from "lucide-react";
import { EditNotifierCard } from "@/components/notifiers/EditNotifierCard";
```

Render the edit card under the Add card (after the `</AnimatePresence>` that wraps `AddNotifierCard`):

```tsx
      <AnimatePresence>
        {editingId && (
          <EditNotifierCard
            key={editingId}
            id={editingId}
            onClose={() => setEditingId(null)}
            onSaved={() => {
              setEditingId(null);
              qc.invalidateQueries({ queryKey: QK.notifiers });
            }}
          />
        )}
      </AnimatePresence>
```

Add an Edit button inside the card's `<div className="mt-4">` next to "Send test":

```tsx
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-2"
                  onClick={() => setEditingId(n.id)}
                >
                  <Pencil className="size-4" />
                  Edit
                </Button>
```

> Note: this Step references `EditNotifierCard` which is created in Task 6. If executing strictly in order, the import will not resolve until Task 6 lands — keep Task 5 and Task 6 in the same review/verify batch, or implement Task 6's file first. The verify step below assumes Task 6 exists.

- [ ] **Step 6: Verify (after Task 6 exists)**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test"`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/Notifiers.tsx
git commit -m "feat: default notifier badge, events, and hint on notifiers page (#85)"
```

---

## Task 6: Frontend EditNotifierCard component

**Files:**
- Create: `frontend/src/components/notifiers/EditNotifierCard.tsx`

**Interfaces:**
- Consumes: `api.getNotifier`, `api.updateNotifier`, `QK.notifier` (Task 4); `fieldsForPlugin` (currently a local helper in `Notifiers.tsx` — export it, see Step 1).
- Produces: `EditNotifierCard({ id, onClose, onSaved })` used by `Notifiers.tsx` (Task 5).

- [ ] **Step 1: Export `fieldsForPlugin` from Notifiers.tsx**

In `frontend/src/pages/Notifiers.tsx`, change `function fieldsForPlugin` and the `Field`/`Plugin` types to exported so the edit card reuses the exact field definitions (DRY — no second copy of plugin fields):

```ts
export type Field = { key: string; label: string; placeholder?: string; password?: boolean };
export function fieldsForPlugin(name: string): Field[] {
```

- [ ] **Step 2: Write the failing component test**

Create `frontend/src/components/notifiers/EditNotifierCard.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { EditNotifierCard } from "./EditNotifierCard";
import { api } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  api: {
    getNotifier: vi.fn(),
    updateNotifier: vi.fn(),
  },
}));

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("EditNotifierCard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("hydrates is_default and sends it on save", async () => {
    (api.getNotifier as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: "n1", notifier_name: "webhook", display_name: "W", events: ["updated"],
      is_default: true, config: { url: "https://e.test/x" },
      created_at: "", updated_at: "",
    });
    (api.updateNotifier as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "n1" });

    wrap(<EditNotifierCard id="n1" onClose={() => {}} onSaved={() => {}} />);

    const defaultCheckbox = await screen.findByLabelText(/default notifier/i);
    expect(defaultCheckbox).toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(api.updateNotifier).toHaveBeenCalledWith(
      "n1",
      expect.objectContaining({ is_default: true, display_name: "W" }),
    ));
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- EditNotifierCard"`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement the component**

Create `frontend/src/components/notifiers/EditNotifierCard.tsx` (mirrors `EditClientCard`):

```tsx
import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { Loader2 } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { fieldsForPlugin } from "@/pages/Notifiers";

interface EditNotifierCardProps {
  id: string;
  onClose: () => void;
  onSaved: () => void;
}

export function EditNotifierCard({ id, onClose, onSaved }: EditNotifierCardProps) {
  const { data, isLoading, isError } = useQuery({
    queryKey: QK.notifier(id),
    queryFn: () => api.getNotifier(id),
  });

  const [displayName, setDisplayName] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [isDefault, setIsDefault] = useState(false);
  const [config, setConfig] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!data) return;
    setDisplayName(data.display_name);
    setEvents(data.events);
    setIsDefault(data.is_default);
    const flat: Record<string, string> = {};
    for (const [k, v] of Object.entries(data.config ?? {})) {
      flat[k] = typeof v === "string" ? v : String(v ?? "");
    }
    setConfig(flat);
  }, [data]);

  const save = useMutation({
    mutationFn: () =>
      api.updateNotifier(id, {
        display_name: displayName,
        events,
        is_default: isDefault,
        config,
      }),
    onSuccess: () => onSaved(),
    onError: (err) => setError(err instanceof Error ? err.message : "Failed"),
  });

  const fields = data ? fieldsForPlugin(data.notifier_name) : [];

  return (
    <motion.div
      initial={{ opacity: 0, y: -8, height: 0 }}
      animate={{ opacity: 1, y: 0, height: "auto" }}
      exit={{ opacity: 0, y: -8, height: 0 }}
      transition={{ duration: 0.2 }}
    >
      <Card className="overflow-hidden">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            save.mutate();
          }}
          className="space-y-4 p-6"
        >
          <h3 className="text-base font-semibold">
            Edit notifier{" "}
            {data && (
              <span className="font-mono text-xs text-muted-foreground">
                ({data.notifier_name})
              </span>
            )}
          </h3>

          {isLoading && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> Loading current config...
            </div>
          )}
          {isError && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              Failed to load notifier config.
            </div>
          )}

          {data && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="edit-ndisplay">Display name</Label>
                <Input
                  id="edit-ndisplay"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  required
                />
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                {fields.map((f) => (
                  <div key={f.key} className="space-y-1.5">
                    <Label htmlFor={`edit-n-${f.key}`}>{f.label}</Label>
                    <Input
                      id={`edit-n-${f.key}`}
                      type={f.password ? "password" : "text"}
                      value={config[f.key] ?? ""}
                      onChange={(e) =>
                        setConfig((c) => ({ ...c, [f.key]: e.target.value }))
                      }
                      placeholder={f.placeholder}
                    />
                  </div>
                ))}
              </div>

              <div className="flex flex-wrap items-center gap-4 text-sm">
                <span className="text-muted-foreground">Notify on:</span>
                {(["updated", "error"] as const).map((ev) => (
                  <label key={ev} className="inline-flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={events.includes(ev)}
                      onChange={(e) =>
                        setEvents((prev) =>
                          e.target.checked ? [...prev, ev] : prev.filter((x) => x !== ev),
                        )
                      }
                    />
                    {ev === "updated" ? "new releases" : "errors"}
                  </label>
                ))}
              </div>

              <label className="inline-flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={isDefault}
                  onChange={(e) => setIsDefault(e.target.checked)}
                />
                Use as a default notifier (one per type)
              </label>
            </>
          )}

          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || !data}>
              {save.isPending && <Loader2 className="size-4 animate-spin" />}
              Save changes
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}
```

> The test's `findByLabelText(/default notifier/i)` matches the "Use as a default notifier (one per type)" label. Keep that label text containing "default notifier".

- [ ] **Step 5: Run component test + full suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
Expected: PASS (this covers Task 5's wiring too — run them together).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/notifiers/EditNotifierCard.tsx frontend/src/components/notifiers/EditNotifierCard.test.tsx frontend/src/pages/Notifiers.tsx
git commit -m "feat: notifier edit dialog with default toggle (#85)"
```

---

## Task 7: Docs — CHANGELOG + CLAUDE.md

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: CHANGELOG**

Under `## [Unreleased]` add an `### Added` and `### Changed` block:

```markdown
### Added

- **Default notifiers** — mark one notifier per type (Telegram, email, …) as
  default with a `default` badge, and edit existing notifiers (display name,
  events, config, default flag) via the Notifiers page. (#85)

### Changed

- **Topic notifications without an explicit notifier now route to your default
  notifiers only** (previously: all configured notifiers). If no default is
  set, such topics send no notification — mark a default on the Notifiers page.
  Per-topic notifier overrides are unaffected. (#85)
```

- [ ] **Step 2: CLAUDE.md**

Update the `notify` and `db / db/repo` rows to note the default-set fallback and the notifier `is_default` (migration `0008`) + `Update`. Keep edits to one line each, matching the existing terse style. Example for the `notify` row — change "nil ⇒ global `Send`" to "nil ⇒ the user's **default** notifiers only (strict; none set ⇒ no send)".

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md CLAUDE.md
git commit -m "docs: record default-notifier feature and routing change (#85)"
```

---

## Final Verification

- [ ] Backend: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.25 sh -c "gofmt -l . && go build ./... && go vet ./... && go test -race ./..."` → gofmt clean, all packages pass.
- [ ] Frontend: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"` → all pass.
- [ ] Manual smoke (dev stack): create two Telegram notifiers, mark one default → the other's default flag clears; mark an email notifier default → both a Telegram and an email show the `default` badge; create a topic with no explicit notifier → only the default notifiers fire; with zero defaults → no notification.

---

## Self-Review

**Spec coverage:**
- "default badge on default notifier" → Task 5 Step 2 (mirrors `Clients.tsx:127`). ✓
- "define a default notifier" → Task 1 (is_default + per-type unique index + auto-unset). ✓
- "used when no notifier selected during topic creation" → Task 3 (`SendVia(nil)` → defaults). ✓
- per-type default + auto-unset same-type → Task 1 `unsetDefaultForType` + index. ✓
- "edit notifiers (only delete today)" → Task 2 (`PUT`/`GET`) + Task 6 (edit dialog). ✓
- strict zero-default behavior → Task 3 `TestSendVia_NilID_NoDefaults_SendsNothing`. ✓
- session-expiry stays global → Task 3 leaves `Send` untouched. ✓
- empty-state safeguard → Task 5 Step 3. ✓

**Type/signature consistency:** `Update(ctx, id, userID uuid.UUID, notifierName, displayName string, events []string, isDefault bool, configEnc, configNonce []byte) error` is identical in repo (Task 1), `notifierStore` seam + handler call (Task 2), and `fakeNotifierStore` (Task 2). `getNotifier`/`updateNotifier`/`NotifierDetail`/`UpdateNotifierBody` (Task 4) match their consumer in `EditNotifierCard` (Task 6). `QK.notifier(id)` defined in Task 4, used in Task 6.

**Cross-task ordering note:** Task 5 imports `EditNotifierCard` (Task 6) and Task 6 imports `fieldsForPlugin` from Task 5's file — they are mutually dependent and MUST be implemented/verified as one batch. The execution controller should treat Tasks 5+6 as a paired unit (implement 6's file and 5's export together before running the frontend suite).

**Placeholder scan:** the only deliberately non-literal items are (a) the handler-test claims-context helper name (`withClaims`) which the implementer must replace with the real helper from `topics_handler_test.go`, and (b) the `api.get`/`request` call-style note — both flagged inline with explicit "read the existing file and match" instructions, not silent gaps.
