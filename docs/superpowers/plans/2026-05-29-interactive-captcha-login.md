# Interactive (captcha) Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user solve a tracker's login captcha inside Marauder, obtain and persist the resulting session cookie, and reuse it for all scheduler checks — starting with LostFilm.

**Architecture:** A reusable `captchalogin` engine (chromedp-free, human-in-the-loop) brokers a stateful login: it POSTs login, fetches the captcha image on a held cookie jar, and replays the login with the user's answer on that same jar. The harvested session cookie (`lf_session`) is encrypted into dedicated `tracker_credentials.session_enc/session_nonce` columns and rehydrated by the plugin's `Login` on each check. LostFilm wires the engine via a small `Config`; the API exposes begin/complete/refresh endpoints; the frontend renders the captcha image and answer field.

**Tech Stack:** Go 1.23 (chi, pgx, zerolog), Postgres (goose migrations), React 19 + Vite + React Query + zustand, Vitest. Build/test via Docker (never install Go/Node locally).

**Reference spec:** `docs/superpowers/specs/2026-05-29-lostfilm-interactive-captcha-login-design.md`

**Conventions to honor:**
- Decrypted-secret convention: callers decrypt `SecretEnc`/`SessionEnc` and overwrite the field with plaintext before handing the credential to a plugin (`scheduler.go:383`). Plugins read plaintext from those fields.
- DTO naming: response shapes are `*View` (read-only) per `~/.claude/rules/dto-naming.md`.
- No synthetic data: tests use stub HTTP servers, never fabricated DB rows.
- Run the full backend gate after each backend task:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./..."`

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/plugins/registry/registry.go` | + `LoginChallenge`, `SessionCookies`, `WithInteractiveLogin` |
| `backend/internal/plugins/registry/errors.go` | + `ErrSessionExpired` |
| `backend/internal/domain/*.go` (TrackerCredential) | + `SessionEnc`, `SessionNonce []byte` |
| `backend/internal/db/migrations/0002_add_session_columns.sql` | new columns |
| `backend/internal/db/repo/tracker_credentials.go` | carry new columns; + `SetSession` |
| `backend/internal/db/repo/tracker_credentials_test.go` | pgxmock round-trip + SetSession |
| `backend/internal/scheduler/scheduler.go` | decrypt `SessionEnc` in `loadCredentials` |
| `backend/internal/plugins/trackers/forumcommon/session.go` | + `CookiesByName` helper |
| `backend/internal/plugins/captchalogin/engine.go` | reusable Begin/Complete/Refresh engine |
| `backend/internal/plugins/captchalogin/pending.go` | TTL pending-session store |
| `backend/internal/plugins/captchalogin/engine_test.go` | engine unit tests |
| `backend/internal/plugins/trackers/lostfilm/lostfilm_session.go` | rewrite `Login`; implement `WithInteractiveLogin` |
| `backend/internal/plugins/trackers/lostfilm/lostfilm_interactive_test.go` | begin/complete/refresh + rehydrate tests |
| `backend/internal/api/handlers/credentials_interactive.go` | begin/complete/refresh handlers + handler pending store |
| `backend/internal/api/handlers/credentials_interactive_test.go` | handler tests |
| `backend/internal/api/router.go` | wire the 3 routes |
| `backend/internal/api/handlers/trackers.go` | + `SupportsInteractiveLogin` |
| `frontend/src/lib/api.ts` | interactive begin/complete/refresh calls + types |
| `frontend/src/lib/queryKeys.ts` | (no change expected; mutations are keyless) |
| `frontend/src/pages/Credentials.tsx` | captcha step in add-credential form |
| `frontend/src/pages/Credentials.test.tsx` | RTL tests |
| `frontend/src/i18n/*` | en/ru strings |
| `CLAUDE.md`, `docs/plugin-development.md` | docs |

---

## Task 1: Verify the live auth round-trip (gate)

**This task is run once by the operator with REAL LostFilm credentials.** Everything else assumes it passes. It confirms the only thing the spike could not: that `GET captcha → POST login+answer` actually authenticates and yields `lf_session`.

- [ ] **Step 1: Capture a captcha bound to a fresh session**

Run (inside the backend container so the egress IP matches production checks):
```bash
docker exec deploy-backend-1 sh -c 'wget -S -qO/tmp/cap.gif --save-cookies=/tmp/j.txt --keep-session-cookies --header="User-Agent: Mozilla/5.0" "https://www.lostfilm.tv/simple_captcha.php?r1" 2>&1 | grep -i set-cookie; echo "jar:"; grep PHPSESSID /tmp/j.txt'
```
Expected: a `Set-Cookie: PHPSESSID=...` line and a `PHPSESSID` row in the jar.

- [ ] **Step 2: Read the captcha image**

Copy it out and open it:
```bash
docker cp deploy-backend-1:/tmp/cap.gif ./cap.gif
```
Read the distorted code from `cap.gif`.

- [ ] **Step 3: Submit login WITH the answer on the SAME jar**

Replace `EMAIL`, `PASS`, `ANSWER`:
```bash
docker exec deploy-backend-1 sh -c 'wget -qO- --load-cookies=/tmp/j.txt --save-cookies=/tmp/j2.txt --keep-session-cookies --post-data="act=users&type=login&mail=EMAIL&pass=PASS&rem=1&need_captcha=1&captcha=ANSWER" --header="Content-Type: application/x-www-form-urlencoded" --header="User-Agent: Mozilla/5.0" "https://www.lostfilm.tv/ajaxik.users.php"; echo; echo "jar:"; grep -E "lf_session|PHPSESSID" /tmp/j2.txt'
```
Expected on success: a JSON body containing `"success"` (and the user's name), and an `lf_session` row in `/tmp/j2.txt`.

- [ ] **Step 4: Record the outcome**

If success + `lf_session` present → proceed. If `{"error":4}` → wrong captcha (retry steps 1-3). If success but **no** `lf_session` cookie → STOP and revisit the spec (the harvested cookie name is wrong). Delete scratch files: `rm -f cap.gif` and `docker exec deploy-backend-1 rm -f /tmp/cap.gif /tmp/j.txt /tmp/j2.txt`.

---

## Task 2: Registry contract

**Files:**
- Modify: `backend/internal/plugins/registry/registry.go`
- Modify: `backend/internal/plugins/registry/errors.go`

- [ ] **Step 1: Add the sentinel**

In `errors.go`, after `ErrCaptchaRequired`:
```go
// ErrSessionExpired is returned by a cookie-session plugin's Login when
// no stored session exists or the stored session no longer authenticates.
// The user must re-run the interactive (captcha) login flow.
var ErrSessionExpired = errors.New("tracker session expired")
```

- [ ] **Step 2: Add the contract types + capability**

In `registry.go`, after the `WithCredentials` interface:
```go
// LoginChallenge is a captcha to present to the user during interactive
// login. Image holds the raw bytes; MIMEType comes from the captcha
// response Content-Type (LostFilm serves image/gif).
type LoginChallenge struct {
	ChallengeID string
	Image       []byte
	MIMEType    string
}

// SessionCookies maps cookie name -> value. It is persisted (encrypted)
// and rehydrated into the plugin's HTTP cookie jar on each check.
type SessionCookies map[string]string

// WithInteractiveLogin is an optional capability for trackers that gate
// login behind a captcha the user must solve. BeginLogin returns exactly
// one of (challenge, cookies): a captcha to solve, or — if the tracker
// did not demand one — the session straight away.
type WithInteractiveLogin interface {
	Tracker
	BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*LoginChallenge, SessionCookies, error)
	CompleteLogin(ctx context.Context, challengeID, answer string) (SessionCookies, error)
	RefreshChallenge(ctx context.Context, challengeID string) (*LoginChallenge, error)
}
```

- [ ] **Step 3: Build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./..."`
Expected: success (no compile errors).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/plugins/registry/
git commit -m "feat(registry): add WithInteractiveLogin capability + ErrSessionExpired"
```

---

## Task 3: Persistence — domain field, migration, repo

**Files:**
- Modify: `backend/internal/domain/<credential file>.go` (the file with `TrackerCredential`)
- Create: `backend/internal/db/migrations/0002_add_session_columns.sql`
- Modify: `backend/internal/db/repo/tracker_credentials.go`
- Test: `backend/internal/db/repo/tracker_credentials_test.go` (create)

- [ ] **Step 1: Add domain fields**

In `TrackerCredential`, after `SecretNonce []byte`:
```go
	SessionEnc   []byte // encrypted JSON cookie map; plaintext JSON in-memory after decrypt
	SessionNonce []byte
```

- [ ] **Step 2: Write the migration**

Create `backend/internal/db/migrations/0002_add_session_columns.sql`:
```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE tracker_credentials ADD COLUMN session_enc   BYTEA;
ALTER TABLE tracker_credentials ADD COLUMN session_nonce BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tracker_credentials DROP COLUMN session_enc;
ALTER TABLE tracker_credentials DROP COLUMN session_nonce;
-- +goose StatementEnd
```

- [ ] **Step 3: Write the failing repo test**

Create `backend/internal/db/repo/tracker_credentials_test.go`:
```go
package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
)

func newMockCreds(t *testing.T) (*TrackerCredentials, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(func() { mock.Close() })
	return &TrackerCredentials{pool: mock}, mock
}

func TestTrackerCredentials_SetSession_UpdatesEncryptedColumns(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	id, userID := uuid.New(), uuid.New()
	enc, nonce := []byte("ct"), []byte("nonce")

	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, enc, nonce).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.SetSession(context.Background(), id, userID, enc, nonce); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
}
```

Note: this requires `TrackerCredentials.pool` to be an interface (like `Topics`). Check the struct — if it is `*pgxpool.Pool`, add a `credsPool` interface seam in Step 4 mirroring `topicsPool` (`Exec`/`Query`/`QueryRow`).

- [ ] **Step 4: Implement repo changes**

In `tracker_credentials.go`:

(a) If `pool` is concrete `*pgxpool.Pool`, introduce the seam at the top:
```go
type credsPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```
and change the struct field + `NewTrackerCredentials` parameter to `credsPool` (imports: `github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgconn`). `*pgxpool.Pool` satisfies it, so callers are unchanged.

(b) Add `session_enc, session_nonce` to the `Create` INSERT column list, the values, and to BOTH `SELECT` column lists (the `scanOne` query and `ListForUser`). Add the scan targets `&c.SessionEnc, &c.SessionNonce` to `scanOne`'s `Scan(...)` in the same column order.

(c) Add the focused updater:
```go
// SetSession overwrites only the encrypted session-cookie blob.
func (r *TrackerCredentials) SetSession(ctx context.Context, id, userID uuid.UUID, sessionEnc, sessionNonce []byte) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE tracker_credentials SET session_enc = $3, session_nonce = $4, updated_at = now() WHERE id = $1 AND user_id = $2`,
		id, userID, sessionEnc, sessionNonce)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
```

- [ ] **Step 5: Run the gate**

Run the full backend gate (build + vet + test -race). Expected: PASS, including `TestTrackerCredentials_SetSession_UpdatesEncryptedColumns`.

- [ ] **Step 6: Apply the migration to the running dev DB**

Restart the backend so goose runs `0002` (migrations run on boot):
```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d backend
docker logs --tail 5 deploy-backend-1 2>&1 | grep -i goose
```
Expected: goose reports applying version 2 (or "no migrations to run. current version: 2").

- [ ] **Step 7: Commit**

```bash
git add backend/internal/domain backend/internal/db/migrations/0002_add_session_columns.sql backend/internal/db/repo/tracker_credentials.go backend/internal/db/repo/tracker_credentials_test.go
git commit -m "feat(credentials): add encrypted session_enc/session_nonce columns + SetSession"
```

---

## Task 4: Scheduler rehydrates the session

**Files:**
- Modify: `backend/internal/scheduler/scheduler.go:378-383` (in `loadCredentials`)

- [ ] **Step 1: Decrypt the session blob in place**

After the existing block that sets `stored.SecretEnc = plain`, add:
```go
	// Rehydrate the persisted session cookie (cookie-session plugins read
	// plaintext JSON from SessionEnc, mirroring the SecretEnc convention).
	if len(stored.SessionEnc) > 0 {
		sessPlain, serr := s.master.Decrypt(stored.SessionEnc, stored.SessionNonce)
		if serr != nil {
			log.Warn().Err(serr).Msg("decrypt session failed")
		} else {
			stored.SessionEnc = sessPlain
		}
	}
```

- [ ] **Step 2: Run the gate**

Full backend gate. Expected: PASS (scheduler tests unaffected — they use credentials without session blobs).

- [ ] **Step 3: Commit**

```bash
git add backend/internal/scheduler/scheduler.go
git commit -m "feat(scheduler): decrypt stored session cookie before plugin Login"
```

---

## Task 5: forumcommon cookie helper

**Files:**
- Modify: `backend/internal/plugins/trackers/forumcommon/session.go`
- Test: add to existing `forumcommon` test file (or create `session_cookies_test.go`)

- [ ] **Step 1: Write the failing test**

Create `backend/internal/plugins/trackers/forumcommon/session_cookies_test.go`:
```go
package forumcommon

import (
	"net/http"
	"net/url"
	"testing"
)

func TestCookiesByName_ReturnsRequestedCookies(t *testing.T) {
	sess := New().GetOrCreate("k", "ua")
	u, _ := url.Parse("https://example.com/")
	sess.Client.Jar.SetCookies(u, []*http.Cookie{
		{Name: "lf_session", Value: "abc"},
		{Name: "PHPSESSID", Value: "xyz"},
	})
	got := CookiesByName(sess, u, []string{"lf_session"})
	if got["lf_session"] != "abc" {
		t.Errorf("lf_session = %q, want abc", got["lf_session"])
	}
	if _, ok := got["PHPSESSID"]; ok {
		t.Error("PHPSESSID should not be harvested")
	}
}
```

- [ ] **Step 2: Run it, expect FAIL**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go test ./internal/plugins/trackers/forumcommon/ -run CookiesByName"`
Expected: FAIL (`CookiesByName` undefined).

- [ ] **Step 3: Implement the helper**

In `session.go`:
```go
// CookiesByName returns the named cookies from the session jar for u as a
// name->value map. Names absent from the jar are simply omitted.
func CookiesByName(s *Session, u *url.URL, names []string) map[string]string {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}
	out := map[string]string{}
	for _, c := range s.Client.Jar.Cookies(u) {
		if _, ok := want[c.Name]; ok {
			out[c.Name] = c.Value
		}
	}
	return out
}
```
Add `"net/url"` to imports if absent.

- [ ] **Step 4: Run it, expect PASS**

Run the same test command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/trackers/forumcommon/
git commit -m "feat(forumcommon): add CookiesByName jar helper"
```

---

## Task 6: captchalogin engine

**Files:**
- Create: `backend/internal/plugins/captchalogin/pending.go`
- Create: `backend/internal/plugins/captchalogin/engine.go`
- Test: `backend/internal/plugins/captchalogin/engine_test.go`

- [ ] **Step 1: Write the pending store**

Create `pending.go`:
```go
// Package captchalogin provides a reusable, human-in-the-loop interactive
// login flow for trackers that gate authentication behind an image
// captcha. A tracker supplies a Config; the Engine handles the stateful
// begin -> (refresh)* -> complete dance and harvests session cookies.
package captchalogin

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const pendingTTL = 5 * time.Minute

type pending struct {
	sess    *forumcommon.Session
	creds   *domain.TrackerCredential
	expires time.Time
}

type pendingStore struct {
	mu  sync.Mutex
	m   map[string]*pending
	now func() time.Time // injectable for tests
}

func newPendingStore() *pendingStore {
	return &pendingStore{m: map[string]*pending{}, now: time.Now}
}

func (p *pendingStore) put(sess *forumcommon.Session, creds *domain.TrackerCredential) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
	p.m[id] = &pending{sess: sess, creds: creds, expires: p.now().Add(pendingTTL)}
	return id, nil
}

func (p *pendingStore) get(id string) (*pending, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
	e, ok := p.m[id]
	return e, ok
}

func (p *pendingStore) del(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, id)
}

func (p *pendingStore) evictLocked() {
	now := p.now()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
}
```

- [ ] **Step 2: Write the engine**

Create `engine.go`:
```go
package captchalogin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

type Outcome int

const (
	OutcomeSuccess Outcome = iota
	OutcomeNeedCaptcha
	OutcomeWrongCaptcha
	OutcomeFailed
)

// ErrWrongCaptcha is returned by Complete when the answer was rejected;
// the pending session is kept so the caller can Refresh and retry.
var ErrWrongCaptcha = errors.New("captcha answer incorrect")

// Config is the tracker-specific configuration for an interactive login.
type Config struct {
	SeedURL     string // optional: GET'd first to seed a session cookie
	LoginURL    string
	CaptchaURL  string
	CookieNames []string
	BuildForm   func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values
	Classify    func(body []byte) Outcome
}

// Engine runs the Config's interactive login flow.
type Engine struct {
	cfg     Config
	store   *pendingStore
	newSess func() *forumcommon.Session // injects a jar (+ test transport)
}

func New(cfg Config, newSess func() *forumcommon.Session) *Engine {
	return &Engine{cfg: cfg, store: newPendingStore(), newSess: newSess}
}

func (e *Engine) post(ctx context.Context, sess *forumcommon.Session, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.LoginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", sess.UserAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}

func (e *Engine) fetchCaptcha(ctx context.Context, sess *forumcommon.Session) (*registry.LoginChallenge, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.CaptchaURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", sess.UserAgent)
	resp, err := sess.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	img, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/gif"
	}
	return &registry.LoginChallenge{Image: img, MIMEType: mime}, mime, nil
}

// Begin starts a login. Returns (challenge, nil) when a captcha is needed,
// or (nil, cookies) when login succeeded outright.
func (e *Engine) Begin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	sess := e.newSess()
	if e.cfg.SeedURL != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.SeedURL, nil)
		req.Header.Set("User-Agent", sess.UserAgent)
		if resp, err := sess.Client.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	body, err := e.post(ctx, sess, e.cfg.BuildForm(creds, "", false))
	if err != nil {
		return nil, nil, fmt.Errorf("interactive begin: %w", err)
	}
	switch e.cfg.Classify(body) {
	case OutcomeSuccess:
		return nil, e.harvest(sess), nil
	case OutcomeNeedCaptcha:
		challenge, _, ferr := e.fetchCaptcha(ctx, sess)
		if ferr != nil {
			return nil, nil, fmt.Errorf("interactive begin: fetch captcha: %w", ferr)
		}
		id, perr := e.store.put(sess, creds)
		if perr != nil {
			return nil, nil, perr
		}
		challenge.ChallengeID = id
		return challenge, nil, nil
	default:
		return nil, nil, errors.New("interactive begin: login rejected")
	}
}

// Refresh re-fetches the captcha image on the existing pending jar.
func (e *Engine) Refresh(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	challenge, _, err := e.fetchCaptcha(ctx, p.sess)
	if err != nil {
		return nil, err
	}
	challenge.ChallengeID = challengeID
	return challenge, nil
}

// Complete submits the answer on the pending jar and harvests cookies.
func (e *Engine) Complete(ctx context.Context, challengeID, answer string) (registry.SessionCookies, error) {
	p, ok := e.store.get(challengeID)
	if !ok {
		return nil, errors.New("unknown or expired challenge")
	}
	body, err := e.post(ctx, p.sess, e.cfg.BuildForm(p.creds, answer, true))
	if err != nil {
		e.store.del(challengeID)
		return nil, fmt.Errorf("interactive complete: %w", err)
	}
	switch e.cfg.Classify(body) {
	case OutcomeSuccess:
		cookies := e.harvest(p.sess)
		e.store.del(challengeID)
		return cookies, nil
	case OutcomeWrongCaptcha:
		return nil, ErrWrongCaptcha // keep pending for Refresh + retry
	default:
		e.store.del(challengeID)
		return nil, errors.New("interactive complete: login rejected")
	}
}

func (e *Engine) harvest(sess *forumcommon.Session) registry.SessionCookies {
	// CookieNames are harvested against the LoginURL host.
	u, _ := url.Parse(e.cfg.LoginURL)
	return registry.SessionCookies(forumcommon.CookiesByName(sess, u, e.cfg.CookieNames))
}
```

- [ ] **Step 3: Write the engine tests**

Create `engine_test.go`:
```go
package captchalogin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func testEngine(t *testing.T, handler http.HandlerFunc) *Engine {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := Config{
		LoginURL:    srv.URL + "/login",
		CaptchaURL:  srv.URL + "/captcha",
		CookieNames: []string{"sid"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			v := url.Values{"mail": {c.Username}, "captcha": {answer}}
			if needCaptcha {
				v.Set("need_captcha", "1")
			}
			return v
		},
		Classify: func(body []byte) Outcome {
			s := string(body)
			switch {
			case strings.Contains(s, "wrong"):
				return OutcomeWrongCaptcha
			case strings.Contains(s, "captcha"):
				return OutcomeNeedCaptcha
			case strings.Contains(s, "ok"):
				return OutcomeSuccess
			default:
				return OutcomeFailed
			}
		},
	}
	return New(cfg, func() *forumcommon.Session { return forumcommon.New().GetOrCreate("t", "ua") })
}

func TestBegin_SuccessNoCaptcha(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S1"})
		w.Write([]byte(`{"result":"ok"}`))
	})
	ch, cookies, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err != nil || ch != nil {
		t.Fatalf("want no challenge, got ch=%v err=%v", ch, err)
	}
	if cookies["sid"] != "S1" {
		t.Errorf("sid = %q, want S1", cookies["sid"])
	}
}

func TestBeginComplete_CaptchaHappyPath(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S2"})
				w.Write([]byte(`{"result":"ok"}`))
				return
			}
			w.Write([]byte(`{"need_captcha":true}`))
		case "/captcha":
			w.Header().Set("Content-Type", "image/gif")
			w.Write([]byte("GIF87a-bytes"))
		}
	})
	ch, _, err := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	if err != nil || ch == nil {
		t.Fatalf("want challenge, got ch=%v err=%v", ch, err)
	}
	if ch.ChallengeID == "" || string(ch.Image) != "GIF87a-bytes" || ch.MIMEType != "image/gif" {
		t.Fatalf("bad challenge: %+v", ch)
	}
	cookies, err := e.Complete(context.Background(), ch.ChallengeID, "1234")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if cookies["sid"] != "S2" {
		t.Errorf("sid = %q, want S2", cookies["sid"])
	}
}

func TestComplete_WrongCaptchaKeepsPending(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				w.Write([]byte(`{"wrong":4}`))
				return
			}
			w.Write([]byte(`{"need_captcha":true}`))
		case "/captcha":
			w.Write([]byte("img"))
		}
	})
	ch, _, _ := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	_, err := e.Complete(context.Background(), ch.ChallengeID, "bad")
	if !errors.Is(err, ErrWrongCaptcha) {
		t.Fatalf("err = %v, want ErrWrongCaptcha", err)
	}
	if _, err := e.Refresh(context.Background(), ch.ChallengeID); err != nil {
		t.Errorf("Refresh after wrong captcha should work, got %v", err)
	}
}

func TestComplete_UnknownChallenge(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := e.Complete(context.Background(), "nope", "x"); err == nil {
		t.Error("want error for unknown challenge")
	}
}

func TestPending_TTLEviction(t *testing.T) {
	e := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"need_captcha":true}`))
		} else {
			w.Write([]byte("img"))
		}
	})
	ch, _, _ := e.Begin(context.Background(), &domain.TrackerCredential{Username: "a"})
	// Force expiry by rewinding stored entry's clock.
	e.store.mu.Lock()
	for _, v := range e.store.m {
		v.expires = v.expires.Add(-2 * pendingTTL)
	}
	e.store.mu.Unlock()
	if _, ok := e.store.get(ch.ChallengeID); ok {
		t.Error("expired entry should have been evicted")
	}
}
```

- [ ] **Step 4: Run the engine tests**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go test -race ./internal/plugins/captchalogin/ -v"`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/plugins/captchalogin/
git commit -m "feat(captchalogin): reusable interactive captcha-login engine"
```

---

## Task 7: LostFilm wiring

**Files:**
- Modify: `backend/internal/plugins/trackers/lostfilm/lostfilm.go` (struct + init: add engine)
- Modify: `backend/internal/plugins/trackers/lostfilm/lostfilm_session.go` (rewrite `Login`; add interactive methods + Classify/BuildForm)
- Test: `backend/internal/plugins/trackers/lostfilm/lostfilm_interactive_test.go` (create)

- [ ] **Step 1: Add the engine to the plugin struct + constructor**

In `lostfilm.go`, add field `engine *captchalogin.Engine` to `plugin`, and build it in `init()` and wherever the plugin is constructed in tests. Because the engine needs sessions that carry the test transport, define a session factory on the plugin:
```go
func (p *plugin) newInteractiveSession() *forumcommon.Session {
	sess := forumcommon.New().GetOrCreate(pluginName+":pending:"+uuid.NewString(), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	return sess
}
```
Lazily construct the engine on first use (so `p.domain`/`p.transport` are set):
```go
func (p *plugin) eng() *captchalogin.Engine {
	if p.engine == nil {
		p.engine = captchalogin.New(p.captchaConfig(), p.newInteractiveSession)
	}
	return p.engine
}
```

- [ ] **Step 2: Add Config, BuildForm, Classify**

In `lostfilm_session.go`:
```go
func (p *plugin) captchaConfig() captchalogin.Config {
	base := "https://" + p.domain
	return captchalogin.Config{
		LoginURL:    base + "/ajaxik.users.php",
		CaptchaURL:  base + "/simple_captcha.php",
		CookieNames: []string{"lf_session"},
		BuildForm: func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values {
			nc := "0"
			if needCaptcha {
				nc = "1"
			}
			return url.Values{
				"act": {"users"}, "type": {"login"},
				"mail": {c.Username}, "pass": {string(c.SecretEnc)},
				"rem": {"1"}, "need_captcha": {nc}, "captcha": {answer},
			}
		},
		Classify: classifyLostfilmLogin,
	}
}

// classifyLostfilmLogin maps LostFilm's ajaxik JSON to an Outcome.
func classifyLostfilmLogin(body []byte) captchalogin.Outcome {
	s := string(body)
	switch {
	case strings.Contains(s, `"error":4`):
		return captchalogin.OutcomeWrongCaptcha
	case strings.Contains(s, `"need_captcha":true`):
		return captchalogin.OutcomeNeedCaptcha
	case strings.Contains(s, `"success"`):
		return captchalogin.OutcomeSuccess
	default:
		return captchalogin.OutcomeFailed
	}
}
```

- [ ] **Step 3: Implement WithInteractiveLogin**

```go
func (p *plugin) BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*registry.LoginChallenge, registry.SessionCookies, error) {
	return p.eng().Begin(ctx, creds)
}
func (p *plugin) CompleteLogin(ctx context.Context, challengeID, answer string) (registry.SessionCookies, error) {
	return p.eng().Complete(ctx, challengeID, answer)
}
func (p *plugin) RefreshChallenge(ctx context.Context, challengeID string) (*registry.LoginChallenge, error) {
	return p.eng().Refresh(ctx, challengeID)
}
```

- [ ] **Step 4: Rewrite Login to rehydrate + validate**

Replace the body of `Login` (the password-POST version) with:
```go
func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || len(creds.SessionEnc) == 0 {
		return fmt.Errorf("lostfilm: no stored session; add the account via the captcha login flow: %w", registry.ErrSessionExpired)
	}
	var cookies map[string]string
	if err := json.Unmarshal(creds.SessionEnc, &cookies); err != nil {
		return fmt.Errorf("lostfilm: corrupt stored session: %w", registry.ErrSessionExpired)
	}
	sess := p.sessions.GetOrCreate(forumcommon.SessionKey(pluginName, creds.UserID.String()), userAgent)
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
	u, _ := url.Parse("https://" + p.domain + "/")
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, val := range cookies {
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: val})
	}
	sess.Client.Jar.SetCookies(u, jarCookies)
	ok, err := p.Verify(ctx, creds)
	if err != nil {
		return fmt.Errorf("lostfilm: session validation: %w", err)
	}
	if !ok {
		return fmt.Errorf("lostfilm: stored session no longer valid; re-run the captcha login flow: %w", registry.ErrSessionExpired)
	}
	sess.LoggedIn = true
	return nil
}
```
Add imports `encoding/json` and ensure `registry` is imported (it is, from the step-1 captcha fix). Remove the now-dead `need_captcha`/`"error"` branches from the old Login body. Keep the captcha-detection comment removed since Login no longer POSTs.

Note: the old `TestLogin_NeedCaptcha_ReturnsActionableError` / `TestLogin_ErrorBody_StillRejected` in `lostfilm_login_test.go` exercised the removed POST path — delete that file (its behavior moves to `classifyLostfilmLogin`, covered below).

- [ ] **Step 5: Write the interactive + rehydrate tests**

Create `lostfilm_interactive_test.go`:
```go
package lostfilm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

func interactivePlugin(t *testing.T, h http.HandlerFunc) *plugin {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &plugin{
		sessions:          forumcommon.New(),
		domain:            "www.lostfilm.tv",
		transport:         &e2etest.HostRewriteTransport{From: "www.lostfilm.tv", To: host, Inner: &allHostsToTest{To: host}},
		redirectValidator: permissiveRedirectValidator,
	}
}

func TestClassifyLostfilmLogin(t *testing.T) {
	cases := map[string]int{
		`{"need_captcha":true,"result":"ok"}`: 1, // NeedCaptcha
		`{"error":4,"result":"ok"}`:           2, // WrongCaptcha
		`{"success":true,"name":"x"}`:         0, // Success
		`{"error":1}`:                         3, // Failed
	}
	for body, want := range cases {
		if got := int(classifyLostfilmLogin([]byte(body))); got != want {
			t.Errorf("classify(%s) = %d, want %d", body, got, want)
		}
	}
}

func TestInteractiveLogin_CaptchaFlowHarvestsSession(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ajaxik.users.php":
			r.ParseForm()
			if r.Form.Get("need_captcha") == "1" {
				http.SetCookie(w, &http.Cookie{Name: "lf_session", Value: "SESS"})
				w.Write([]byte(`{"success":true,"name":"alice"}`))
				return
			}
			w.Write([]byte(`{"need_captcha":true,"result":"ok"}`))
		case "/simple_captcha.php":
			w.Header().Set("Content-Type", "image/gif")
			w.Write([]byte("GIF87a"))
		}
	})
	creds := &domain.TrackerCredential{UserID: uuid.New(), Username: "alice@example.com", SecretEnc: []byte("pw")}
	ch, cookies, err := p.BeginLogin(context.Background(), creds)
	if err != nil || ch == nil || cookies != nil {
		t.Fatalf("want challenge, got ch=%v cookies=%v err=%v", ch, cookies, err)
	}
	got, err := p.CompleteLogin(context.Background(), ch.ChallengeID, "1234")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if got["lf_session"] != "SESS" {
		t.Errorf("lf_session = %q, want SESS", got["lf_session"])
	}
}

func TestLogin_NoStoredSession_ReturnsErrSessionExpired(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {})
	err := p.Login(context.Background(), &domain.TrackerCredential{UserID: uuid.New()})
	if !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestLogin_RehydratesAndValidates(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/my" {
			w.Write([]byte(`<a href="/logout">logout</a>`))
		}
	})
	sessionJSON, _ := json.Marshal(map[string]string{"lf_session": "SESS"})
	creds := &domain.TrackerCredential{UserID: uuid.New(), SessionEnc: sessionJSON}
	if err := p.Login(context.Background(), creds); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestLogin_DeadSession_ReturnsErrSessionExpired(t *testing.T) {
	p := interactivePlugin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/my" {
			w.Write([]byte(`<html>please log in</html>`)) // no "logout" marker
		}
	})
	sessionJSON, _ := json.Marshal(map[string]string{"lf_session": "DEAD"})
	creds := &domain.TrackerCredential{UserID: uuid.New(), SessionEnc: sessionJSON}
	if err := p.Login(context.Background(), creds); !errors.Is(err, registry.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}
```

- [ ] **Step 6: Delete the obsolete login test + run lostfilm tests**

```bash
git rm backend/internal/plugins/trackers/lostfilm/lostfilm_login_test.go
```
Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./internal/plugins/trackers/lostfilm/ -v"`
Expected: all PASS (TestE2E still green — it stubs `/ajaxik.php` for the existing Check/Download path which is unchanged; the new tests cover the interactive + rehydrate paths).

If `TestE2E`'s login step breaks because `Login` no longer POSTs: update its `Creds` to carry a `SessionEnc` JSON (`{"lf_session":"x"}`) and have the stub serve `/my` with a `logout` marker, so rehydrate+verify passes. Make that edit in the same step.

- [ ] **Step 7: Run the full gate + commit**

Full backend gate. Expected: PASS.
```bash
git add backend/internal/plugins/trackers/lostfilm/
git commit -m "feat(lostfilm): interactive captcha login + cookie-session rehydration"
```

---

## Task 8: API endpoints

**Files:**
- Create: `backend/internal/api/handlers/credentials_interactive.go`
- Test: `backend/internal/api/handlers/credentials_interactive_test.go`
- Modify: `backend/internal/api/router.go:146-150` (add routes)

- [ ] **Step 1: Write the handler (begin/complete/refresh + handler pending store)**

Create `credentials_interactive.go`:
```go
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/audit"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/captchalogin"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/problem"
)

// handlerPending holds non-secret correlation metadata for an in-flight
// interactive login. The password lives only in the plugin engine's
// pending jar, never here.
type handlerPending struct {
	userID      uuid.UUID
	trackerName string
	username    string
	expires     time.Time
}

type interactivePendingStore struct {
	mu sync.Mutex
	m  map[string]*handlerPending
}

func newInteractivePendingStore() *interactivePendingStore {
	return &interactivePendingStore{m: map[string]*handlerPending{}}
}

func (s *interactivePendingStore) put(challengeID string, p *handlerPending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[challengeID] = p
}

func (s *interactivePendingStore) get(challengeID string, userID uuid.UUID) (*handlerPending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[challengeID]
	if !ok || time.Now().After(p.expires) || p.userID != userID {
		return nil, false
	}
	return p, true
}

func (s *interactivePendingStore) del(challengeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, challengeID)
}

type beginReq struct {
	TrackerName string `json:"tracker_name"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}
type answerReq struct {
	TrackerName string `json:"tracker_name"`
	ChallengeID string `json:"challenge_id"`
	Answer      string `json:"answer"`
}
type refreshReq struct {
	TrackerName string `json:"tracker_name"`
	ChallengeID string `json:"challenge_id"`
}

func dataURL(mime string, img []byte) string {
	if mime == "" {
		mime = "image/gif"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)
}

func (h *Credentials) interactivePlugin(trackerName string) (registry.WithInteractiveLogin, *problem.Error) {
	t := registry.GetTracker(trackerName)
	if t == nil {
		return nil, problem.ErrUnprocessable("unknown tracker plugin: " + trackerName)
	}
	wi, ok := t.(registry.WithInteractiveLogin)
	if !ok {
		return nil, problem.ErrUnprocessable("tracker '" + trackerName + "' does not support interactive login")
	}
	return wi, nil
}

func (h *Credentials) persistSession(ctx context.Context, uid uuid.UUID, trackerName, username string, cookies registry.SessionCookies) (*domain.TrackerCredential, error) {
	cookieJSON, err := json.Marshal(cookies)
	if err != nil {
		return nil, err
	}
	enc, nonce, err := h.Master.Encrypt(cookieJSON)
	if err != nil {
		return nil, err
	}
	return h.Creds.Create(ctx, &domain.TrackerCredential{
		UserID: uid, TrackerName: trackerName, Username: username,
		SessionEnc: enc, SessionNonce: nonce,
	})
}

// BeginInteractive handles POST /credentials/interactive/begin.
func (h *Credentials) BeginInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req beginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	if req.TrackerName == "" || req.Username == "" || req.Password == "" {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("tracker_name, username, and password are required"))
		return
	}
	wi, perr := h.interactivePlugin(req.TrackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	transient := &domain.TrackerCredential{UserID: uid, TrackerName: req.TrackerName, Username: req.Username, SecretEnc: []byte(req.Password)}
	challenge, cookies, err := wi.BeginLogin(r.Context(), transient)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	if cookies != nil { // trusted IP, no captcha
		created, cerr := h.persistSession(r.Context(), uid, req.TrackerName, req.Username, cookies)
		if cerr != nil {
			problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist session: "+cerr.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "logged_in", "credential": toCredView(created)})
		return
	}
	h.pending.put(challenge.ChallengeID, &handlerPending{userID: uid, trackerName: req.TrackerName, username: req.Username, expires: time.Now().Add(5 * time.Minute)})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "captcha", "challenge_id": challenge.ChallengeID,
		"captcha_image": dataURL(challenge.MIMEType, challenge.Image),
	})
}

// CompleteInteractive handles POST /credentials/interactive/complete.
func (h *Credentials) CompleteInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req answerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	p, ok := h.pending.get(req.ChallengeID, uid)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired"))
		return
	}
	wi, perr := h.interactivePlugin(p.trackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	cookies, err := wi.CompleteLogin(r.Context(), req.ChallengeID, req.Answer)
	if err != nil {
		if errors.Is(err, captchalogin.ErrWrongCaptcha) {
			problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("captcha_incorrect"))
			return
		}
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	created, cerr := h.persistSession(r.Context(), uid, p.trackerName, p.username, cookies)
	if cerr != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist session: "+cerr.Error()))
		return
	}
	h.pending.del(req.ChallengeID)
	if h.Audit != nil {
		ip, ua := audit.FromRequest(r)
		h.Audit.Generic(&uid, "credential_create", "tracker_credential", created.ID.String(), "success",
			map[string]any{"tracker_name": p.trackerName, "interactive": true, "ip": ip, "ua": ua})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"credential": toCredView(created)})
}

// RefreshInteractive handles POST /credentials/interactive/refresh.
func (h *Credentials) RefreshInteractive(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	var req refreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON"))
		return
	}
	p, ok := h.pending.get(req.ChallengeID, uid)
	if !ok {
		problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired"))
		return
	}
	wi, perr := h.interactivePlugin(p.trackerName)
	if perr != nil {
		problem.Write(w, r, h.BaseURL, perr)
		return
	}
	challenge, err := wi.RefreshChallenge(r.Context(), req.ChallengeID)
	if err != nil {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": challenge.ChallengeID,
		"captcha_image": dataURL(challenge.MIMEType, challenge.Image),
	})
}

var _ = hex.EncodeToString // retained if needed; remove if unused
var _ = rand.Read
```
Note: drop the trailing `var _ =` lines if the imports end up unused — `go vet` will tell you. (They are only there as a reminder; remove `crypto/rand` and `encoding/hex` from imports if not referenced.)

- [ ] **Step 2: Add the pending store to the Credentials handler struct**

In `credentials.go`, add to `type Credentials struct`:
```go
	pending *interactivePendingStore
```
and ensure it is initialized where the handler is built in `router.go` (next step). To avoid a nil store, add a lazy accessor used by the handlers instead of the field directly, OR initialize in router. Use router init (Step 4).

- [ ] **Step 3: Write the handler tests**

Create `credentials_interactive_test.go` with a fake `WithInteractiveLogin` registered in the registry, exercising: begin→captcha (200, challenge_id + data URL), begin→logged_in (no captcha), complete success (201), complete wrong-answer (422 `captcha_incorrect`), complete with foreign userID (404). Use `httptest.NewRequest` + the real `Credentials` handler with an in-memory `repo.TrackerCredentials` backed by pgxmock, and a real `MasterKey` from `crypto`. Model the structure on the existing `credentials_test.go`. Minimum assertions:
```go
// fakeInteractive implements registry.Tracker + WithInteractiveLogin.
// BeginLogin returns a challenge on first call; CompleteLogin returns
// cookies for answer=="good", ErrWrongCaptcha otherwise.
```
(Write concrete table cases mirroring the engine tests; assert HTTP status + JSON `status`/`challenge_id` fields.)

- [ ] **Step 4: Wire routes + init pending store**

In `router.go`, where `credsH := &handlers.Credentials{...}` is built (~line 100), set `pending` via a constructor. Add an exported constructor in `credentials.go`:
```go
func NewCredentials(creds *repo.TrackerCredentials, master *crypto.MasterKey, auditLog *audit.Logger, baseURL string) *Credentials {
	return &Credentials{Creds: creds, Master: master, Audit: auditLog, BaseURL: baseURL, pending: newInteractivePendingStore()}
}
```
Replace the struct literal in `router.go` with `handlers.NewCredentials(...)`. Then add routes alongside the existing credentials routes (~line 150):
```go
			r.Post("/credentials/interactive/begin", credsH.BeginInteractive)
			r.Post("/credentials/interactive/complete", credsH.CompleteInteractive)
			r.Post("/credentials/interactive/refresh", credsH.RefreshInteractive)
```

- [ ] **Step 5: Run the gate**

Full backend gate. Expected: PASS including the new handler tests.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/api/
git commit -m "feat(api): interactive credential login endpoints (begin/complete/refresh)"
```

---

## Task 9: Capability discovery

**Files:**
- Modify: `backend/internal/api/handlers/trackers.go` (`trackerMatch` + `Match`)

- [ ] **Step 1: Add the flag**

In `trackerMatch` struct, add:
```go
	SupportsInteractiveLogin bool `json:"supports_interactive_login"`
```
In `Match`, after the `WithCredentials` check:
```go
	if _, ok := t.(registry.WithInteractiveLogin); ok {
		out.SupportsInteractiveLogin = true
	}
```

- [ ] **Step 2: Verify live**

Run the gate, then:
```bash
curl -s "http://localhost:34080/api/v1/trackers/match?url=https://www.lostfilm.tv/series/Test/" -H "Authorization: Bearer <token>" | grep interactive
```
(Or assert via an existing `trackers` handler test if present.) Expected: `"supports_interactive_login":true`.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/api/handlers/trackers.go
git commit -m "feat(api): advertise supports_interactive_login in /trackers/match"
```

---

## Task 10: Frontend captcha flow

**Files:**
- Modify: `frontend/src/lib/api.ts` (types + 3 calls)
- Modify: `frontend/src/pages/Credentials.tsx` (captcha step)
- Modify: `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`
- Test: `frontend/src/pages/Credentials.test.tsx`

Frontend gate after this task:
`docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`

- [ ] **Step 1: Add API client methods + types**

In `api.ts`, add (matching the existing `api` object + `ApiError` patterns):
```ts
export interface InteractiveBeginResult {
  status: "logged_in" | "captcha";
  challenge_id?: string;
  captcha_image?: string;
  credential?: CredentialView;
}
export interface InteractiveCompleteResult { credential: CredentialView; }

// inside the api object:
interactiveBegin: (body: { tracker_name: string; username: string; password: string }) =>
  request<InteractiveBeginResult>("POST", "/credentials/interactive/begin", body),
interactiveComplete: (body: { tracker_name: string; challenge_id: string; answer: string }) =>
  request<InteractiveCompleteResult>("POST", "/credentials/interactive/complete", body),
interactiveRefresh: (body: { tracker_name: string; challenge_id: string }) =>
  request<{ challenge_id: string; captcha_image: string }>("POST", "/credentials/interactive/refresh", body),
```
(Use the existing `CredentialView`/`credentialView` type name already in `api.ts`; if absent, define a minimal interface matching `credentialView` fields.)

- [ ] **Step 2: Add i18n strings**

In `en.ts` add keys (and Russian equivalents in `ru.ts`):
```ts
credentials: {
  // ...existing...
  captchaTitle: "Enter the code from the image",
  captchaPlaceholder: "Code from the image",
  captchaRefresh: "Refresh code",
  captchaIncorrect: "Incorrect code, please try again",
  loginAndSave: "Login & save",
}
```

- [ ] **Step 3: Write the failing component test**

Create `Credentials.test.tsx` (model on existing page tests + `frontend/src/test/setup.ts`). Mock `api.interactiveBegin` to resolve `{status:"captcha", challenge_id:"c1", captcha_image:"data:image/gif;base64,Rk="}` and assert the captcha `<img>` and answer input render after submitting email+password for an interactive tracker. Mock `api.interactiveComplete` to resolve `{credential:{...}}` and assert the form closes. Mock a 422 `captcha_incorrect` and assert the inline error + that `api.interactiveRefresh` is called.

- [ ] **Step 4: Run it, expect FAIL**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- Credentials"`
Expected: FAIL (captcha UI not implemented).

- [ ] **Step 5: Implement the captcha step in Credentials.tsx**

In the add-credential form, when the selected tracker's match has `supports_interactive_login`:
- Submitting email+password calls `api.interactiveBegin`. On `logged_in` → `queryClient.invalidateQueries({queryKey: QK.credentials})` + close. On `captcha` → store `{challengeId, captchaImage}` in local state and render:
```tsx
{challenge && (
  <div className="space-y-2">
    <img src={challenge.captchaImage} alt={t("credentials.captchaTitle")} className="rounded border" />
    <button type="button" onClick={handleRefresh} className="text-xs text-muted-foreground">
      {t("credentials.captchaRefresh")}
    </button>
    <input value={answer} onChange={(e) => setAnswer(e.target.value)}
      placeholder={t("credentials.captchaPlaceholder")} className="..." />
    {captchaError && <p className="text-sm text-destructive">{t("credentials.captchaIncorrect")}</p>}
  </div>
)}
```
- The submit button, when `challenge` is set, calls `api.interactiveComplete({tracker_name, challenge_id: challenge.challengeId, answer})`. On success → invalidate + close. On `ApiError` with detail `captcha_incorrect` → set `captchaError`, then call `api.interactiveRefresh` and swap in the new image.
- `handleRefresh` calls `api.interactiveRefresh` and replaces `challenge.captchaImage`.
- Non-interactive trackers keep the existing single `api.createCredential` path untouched (branch on the capability flag from the tracker match query).

Use `react-hook-form` state already present; keep added `useState` count within the 8-call limit by grouping `{challengeId, captchaImage, answer, captchaError}` into one `useState<CaptchaState|null>` object.

- [ ] **Step 6: Run frontend gate, expect PASS**

Run the frontend gate. Expected: typecheck, tests (incl. new Credentials tests), and build all PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src
git commit -m "feat(frontend): in-app captcha login flow for interactive trackers"
```

---

## Task 11: Manual end-to-end verification + docs

**Files:**
- Modify: `CLAUDE.md`, `docs/plugin-development.md`

- [ ] **Step 1: Manual E2E (the real success check)**

With the stack up, open http://localhost:34080 → Credentials → add a LostFilm account with your real email + password. Solve the captcha shown in-app. Expected: the credential is saved; a subsequent topic Check for a LostFilm series succeeds (no `auth failed`). Confirm via:
```bash
docker logs --tail 30 deploy-backend-1 2>&1 | grep -iE "lostfilm|auth failed|scheduler"
```

- [ ] **Step 2: Update CLAUDE.md**

Add a `captchalogin` row to the backend package table ("reusable interactive captcha-login engine; first consumer LostFilm"), note the `WithInteractiveLogin` capability in the registry row, and the `session_enc/session_nonce` credential columns. Note the `/credentials/interactive/*` endpoints.

- [ ] **Step 3: Update docs/plugin-development.md**

Add a short "Interactive (captcha) login" section: implement `registry.WithInteractiveLogin` by embedding a `captchalogin.Engine` built from a `captchalogin.Config` (LoginURL, CaptchaURL, CookieNames, BuildForm, Classify); persist via the `/credentials/interactive/*` flow; rewrite `Login` to rehydrate from `SessionEnc` and validate via `Verify`.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md docs/plugin-development.md
git commit -m "docs: document interactive captcha login (captchalogin + WithInteractiveLogin)"
```

---

## Self-Review notes (addressed during planning)

- **Spec coverage:** data model (Task 3), engine + reuse (Task 6), LostFilm wiring incl. rewritten Login (Task 7), API begin/complete/refresh (Task 8), capability discovery (Task 9), frontend (Task 10), docs (Task 11), live round-trip gate (Task 1), scheduler rehydration (Task 4), forumcommon helper (Task 5). All spec sections map to a task.
- **Decrypt path:** Task 4 mirrors the existing `SecretEnc = plain` convention so the plugin reads plaintext session JSON from `SessionEnc` without needing the master key.
- **Type consistency:** `LoginChallenge{ChallengeID,Image,MIMEType}`, `SessionCookies`, `Outcome`, `ErrWrongCaptcha`, `ErrSessionExpired`, `captchaConfig`/`classifyLostfilmLogin`, `SetSession`, `NewCredentials`, `interactivePendingStore` used consistently across tasks.
- **Test-server caveat:** all backend tests use `httptest`; no fabricated DB rows (no-synthetic-data rule). The single real-credential step (Task 1, Task 11) is operator-run, not committed.
