# Session Re-auth + Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Notify the user (via all configured notifiers) when a tracker session expires, and let them re-authenticate by solving only a captcha — no email/password re-entry.

**Architecture:** Persist the tracker password (encrypted) so re-auth has it; build the first notification-dispatch layer (`notify.Dispatcher`) and wire the scheduler to fire a one-shot "session expired" alert (deduped via a `session_expired_at` marker); add captcha-only `/credentials/{id}/reauth/{begin,complete}` endpoints + a credential-row badge & dialog.

**Tech Stack:** Go 1.23 (chi, pgx, zerolog, Prometheus), Postgres (goose), React 19 + Vite + TS + React Query, Vitest. Build/test via Docker.

**Spec:** `docs/superpowers/specs/2026-05-29-session-reauth-and-notifications-design.md`

**Conventions:** consumer-side interface seams (like scheduler's `topicsRepo`); decrypted-secret convention (caller decrypts, plugin reads plaintext from the field); `*View` DTO naming; no synthetic data; gofmt-clean changed files (3 pre-existing unrelated gofmt violations in the tree are out of scope). Backend gate after each backend task:
`docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go build ./... && go vet ./... && go test -race ./..."`

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/db/migrations/0003_add_session_expired_at.sql` | new nullable column |
| `backend/internal/domain/*.go` (TrackerCredential) | + `SessionExpiredAt *time.Time` |
| `backend/internal/db/repo/tracker_credentials.go` | select new column; `MarkSessionExpired`; `SetSession` clears it |
| `backend/internal/notify/dispatcher.go` | NEW reusable notification dispatch |
| `backend/internal/notify/dispatcher_test.go` | dispatcher unit tests |
| `backend/internal/scheduler/scheduler.go` | `notifiersRepo` seam + dispatcher dep + expiry notify/dedup |
| `backend/cmd/server/main.go` | construct dispatcher, pass notifiers repo to scheduler |
| `backend/internal/api/handlers/credentials_interactive.go` | persist password; reauth begin/complete handlers |
| `backend/internal/api/handlers/credentials.go` | `credentialView.session_expired`; interface += MarkSessionExpired |
| `backend/internal/api/router.go` | wire reauth routes |
| `frontend/src/lib/api.ts` | reauth calls; `CredentialView.session_expired` |
| `frontend/src/pages/Credentials.tsx` | badge + captcha-only reauth dialog |
| `frontend/src/i18n/{en,ru}.ts` | reauth strings |
| `CLAUDE.md`, `docs/plugin-development.md`, `techdebt/frontend/3-2-...` | docs |

---

## Task 1: Persistence — session_expired_at column + repo

**Files:** migration `0003_add_session_expired_at.sql` (create); `domain` TrackerCredential; `db/repo/tracker_credentials.go`; `tracker_credentials_test.go`.

- [ ] **Step 1: Migration**

Create `backend/internal/db/migrations/0003_add_session_expired_at.sql`:
```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE tracker_credentials ADD COLUMN session_expired_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tracker_credentials DROP COLUMN session_expired_at;
-- +goose StatementEnd
```

- [ ] **Step 2: Domain field**

In `TrackerCredential`, after `SessionNonce []byte`:
```go
	SessionExpiredAt *time.Time // non-nil when the stored session failed validation; cleared on re-auth
```

- [ ] **Step 3: Failing repo tests**

Add to `tracker_credentials_test.go` (same package, pgxmock — mirror existing `newMockCreds`):
```go
func TestTrackerCredentials_MarkSessionExpired(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() { if err := mock.ExpectationsWereMet(); err != nil { t.Errorf("unfulfilled: %v", err) } })
	id, userID := uuid.New(), uuid.New()
	mock.ExpectExec(`UPDATE tracker_credentials SET session_expired_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := repo.MarkSessionExpired(context.Background(), id, userID); err != nil { t.Fatalf("MarkSessionExpired: %v", err) }
}

func TestTrackerCredentials_SetSession_ClearsExpiredMarker(t *testing.T) {
	repo, mock := newMockCreds(t)
	t.Cleanup(func() { if err := mock.ExpectationsWereMet(); err != nil { t.Errorf("unfulfilled: %v", err) } })
	id, userID := uuid.New(), uuid.New()
	mock.ExpectExec(`UPDATE tracker_credentials SET session_enc = \$3, session_nonce = \$4, session_expired_at = NULL, updated_at = now\(\) WHERE id = \$1 AND user_id = \$2`).
		WithArgs(id, userID, []byte("ct"), []byte("n")).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := repo.SetSession(context.Background(), id, userID, []byte("ct"), []byte("n")); err != nil { t.Fatalf("SetSession: %v", err) }
}
```

- [ ] **Step 4: Run, expect FAIL** (`MarkSessionExpired` undefined; SetSession SQL lacks the clear).

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/backend:/backend" -w //backend golang:1.23 sh -c "go test ./internal/db/repo/ -run 'MarkSessionExpired|SetSession'"`

- [ ] **Step 5: Implement**

In `tracker_credentials.go`:
- Add `session_expired_at` to both SELECT column lists (scanOne + ListForUser) and add scan target `&c.SessionExpiredAt` to `scanCred` in the matching column order (after `session_nonce`, before `extra`).
- Update `SetSession`'s SQL to also clear the marker:
  `UPDATE tracker_credentials SET session_enc = $3, session_nonce = $4, session_expired_at = NULL, updated_at = now() WHERE id = $1 AND user_id = $2`
- Add:
```go
// MarkSessionExpired flags a credential's stored session as no longer
// valid (used by the scheduler to dedupe expiry notifications). Cleared by
// SetSession on successful re-auth.
func (r *TrackerCredentials) MarkSessionExpired(ctx context.Context, id, userID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE tracker_credentials SET session_expired_at = now() WHERE id = $1 AND user_id = $2`,
		id, userID)
	if err != nil { return err }
	if ct.RowsAffected() == 0 { return ErrNotFound }
	return nil
}
```

- [ ] **Step 6: Run gate** (full backend gate). Expected PASS.

- [ ] **Step 7: Apply migration** — `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml up -d backend` then `docker logs --tail 5 deploy-backend-1 2>&1 | grep -i goose` (expect version 3).

- [ ] **Step 8: Commit** — `git add backend/internal/domain backend/internal/db && git commit -m "feat(credentials): session_expired_at marker + MarkSessionExpired"`

---

## Task 2: notify.Dispatcher

**Files:** `backend/internal/notify/dispatcher.go` (create); `dispatcher_test.go`.

Context: `repo.Notifiers.ListForUser(ctx, userID) ([]*domain.Notifier, error)`; `domain.Notifier{NotifierName, ConfigEnc, ConfigNonce []byte, ...}`; `registry.GetNotifier(name) Notifier`; `registry.Notifier.Send(ctx, rawConfig []byte, msg domain.Message) error`; `crypto.MasterKey.Decrypt(ct, nonce) ([]byte, error)`; `domain.Message{Title, Body, Link}`.

- [ ] **Step 1: Implement the dispatcher**
```go
// Package notify dispatches domain.Message notifications to a user's
// configured notifier plugins. It is the single event->notifier fan-out
// point (first consumer: scheduler session-expiry alerts).
package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/artyomsv/marauder/backend/internal/crypto"
	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/metrics"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// notifiersRepo is the consumer-side seam over *repo.Notifiers.
type notifiersRepo interface {
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Notifier, error)
}

type Dispatcher struct {
	notifiers notifiersRepo
	master    *crypto.MasterKey
	log       zerolog.Logger
	timeout   time.Duration
}

func New(notifiers notifiersRepo, master *crypto.MasterKey, log zerolog.Logger) *Dispatcher {
	return &Dispatcher{notifiers: notifiers, master: master, log: log, timeout: 15 * time.Second}
}

// Send delivers msg to every notifier configured by userID. Best-effort:
// each notifier is attempted independently; failures are logged and
// metered but never abort the others. Returns the count of successes.
func (d *Dispatcher) Send(ctx context.Context, userID uuid.UUID, msg domain.Message) int {
	list, err := d.notifiers.ListForUser(ctx, userID)
	if err != nil {
		d.log.Warn().Err(err).Str("user_id", userID.String()).Msg("notify: list notifiers failed")
		return 0
	}
	sent := 0
	for _, n := range list {
		plugin := registry.GetNotifier(n.NotifierName)
		if plugin == nil {
			d.log.Warn().Str("notifier", n.NotifierName).Msg("notify: unknown notifier plugin")
			continue
		}
		raw, derr := d.master.Decrypt(n.ConfigEnc, n.ConfigNonce)
		if derr != nil {
			d.log.Warn().Err(derr).Str("notifier", n.NotifierName).Msg("notify: decrypt config failed")
			metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "error").Inc()
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, d.timeout)
		serr := plugin.Send(sctx, raw, msg)
		cancel()
		if serr != nil {
			d.log.Warn().Err(serr).Str("notifier", n.NotifierName).Msg("notify: send failed")
			metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "error").Inc()
			continue
		}
		metrics.NotificationsSentTotal.WithLabelValues(n.NotifierName, "ok").Inc()
		sent++
	}
	return sent
}
```

- [ ] **Step 2: Add the metric**

In `backend/internal/metrics/` (find the collectors file; mirror an existing CounterVec):
```go
// NotificationsSentTotal counts notifier dispatch attempts by result.
var NotificationsSentTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{Name: "marauder_notifications_sent_total", Help: "Notification dispatch attempts by notifier and result."},
	[]string{"notifier", "result"},
)
```
Register it where the package registers its other collectors (find the `MustRegister`/`init` block and add it).

- [ ] **Step 3: Tests**

`dispatcher_test.go`: a fake `notifiersRepo` returning N `*domain.Notifier`, and fake notifiers registered in the registry (under unique names via `init()`/once) whose `Send` is programmable (succeed / error). Assert: sends to all; one failing notifier doesn't stop the others; success count correct; empty list → 0; unknown plugin name skipped. Use a real `crypto.LoadMasterKey(base64 of 32 zero bytes)` and pre-encrypt a tiny config with it so `Decrypt` succeeds.

- [ ] **Step 4: Gate + commit** — `git add backend/internal/notify backend/internal/metrics && git commit -m "feat(notify): reusable notification dispatcher"`

---

## Task 3: Scheduler — expiry detection, dedup, notify

**Files:** `scheduler.go`; `cmd/server/main.go`; `scheduler_test.go`.

- [ ] **Step 1: Add deps**

In `scheduler.go`: add a `notifiersRepo` consumer interface (or reuse a `notifier dispatcher` interface) and a dispatcher field. Cleanest: hold a small interface
```go
type notifier interface {
	Send(ctx context.Context, userID uuid.UUID, msg domain.Message) int
}
```
on the Scheduler struct (a `*notify.Dispatcher` satisfies it). Add a `marker` seam for the credential mark/clear — extend the existing `credentialsRepo` consumer interface with `MarkSessionExpired(ctx, id, userID uuid.UUID) error`.

Change `scheduler.New(cfg, log, topics, clients, creds, master)` →
`scheduler.New(cfg, log, topics, clients, creds, master, notifier)` (append the dispatcher). Update `cmd/server/main.go` to construct
`disp := notify.New(notifiersRepo, master, logger)` and pass it.

- [ ] **Step 2: Failing scheduler test**

In `scheduler_test.go` mirror existing fakes. Add a fake credentials repo whose `Login` (via the tracker fake) returns `registry.ErrSessionExpired`, a fake notifier recording sends, and assert in `loadCredentials`:
- first expired check → `MarkSessionExpired` called once AND notifier.Send called once;
- when `SessionExpiredAt` already set on the stored cred → neither called again.
(Construct `stored.SessionExpiredAt` non-nil for the dedup case.)

- [ ] **Step 3: Implement in `loadCredentials`**

Where `wc.Login(...)` returns an error, replace the current error handling with:
```go
	if loginErr := wc.Login(checkCtx, stored); loginErr != nil {
		if errors.Is(loginErr, registry.ErrSessionExpired) && stored.SessionExpiredAt == nil {
			if merr := s.creds.MarkSessionExpired(ctx, stored.ID, stored.UserID); merr != nil {
				log.Warn().Err(merr).Msg("mark session expired failed")
			}
			s.notifier.Send(ctx, stored.UserID, domain.Message{
				Title: "Tracker session expired",
				Body:  t.TrackerName + " needs re-authentication — solve the captcha in Marauder.",
				Link:  s.cfg.PublicBaseURL + "/credentials",
			})
		}
		log.Warn().Err(loginErr).Msg("tracker login failed")
		metrics.SchedulerTopicChecksTotal.WithLabelValues(t.TrackerName, "auth_error").Inc()
		s.recordResult(ctx, log, t.ID, "", false, s.backoff(t, true), "auth failed: "+loginErr.Error())
		s.recordChecked(false, true)
		return nil, false
	}
```
Ensure `stored.ID` is available (GetForTracker selects id — it does via scanCred). Add `errors` + `domain` imports if missing.

- [ ] **Step 4: Gate + commit** — `git add backend/internal/scheduler backend/cmd/server/main.go && git commit -m "feat(scheduler): notify once on session expiry (deduped)"`

---

## Task 4: Persist the password on interactive add

**Files:** `credentials_interactive.go`; `credentials_interactive_test.go`.

- [ ] **Step 1: Carry password begin→complete**

In `handlerPending` add `password string`. In `BeginInteractive`, when storing the pending (captcha branch), include `password: req.Password`.

- [ ] **Step 2: persistSession stores the password too**

Change `persistSession` to accept the password and encrypt it into `secret_enc`/`secret_nonce` on Create:
```go
func (h *Credentials) persistSession(ctx context.Context, uid uuid.UUID, trackerName, username, password string, cookies registry.SessionCookies) (*domain.TrackerCredential, error) {
	cookieJSON, err := json.Marshal(cookies)
	if err != nil { return nil, err }
	sEnc, sNonce, err := h.Master.Encrypt(cookieJSON)
	if err != nil { return nil, err }
	if existing, gerr := h.Creds.GetForTracker(ctx, uid, trackerName); gerr == nil && existing != nil {
		if serr := h.Creds.SetSession(ctx, existing.ID, uid, sEnc, sNonce); serr != nil { return nil, serr }
		existing.SessionEnc, existing.SessionNonce = sEnc, sNonce
		return existing, nil
	}
	pEnc, pNonce, err := h.Master.Encrypt([]byte(password))
	if err != nil { return nil, err }
	return h.Creds.Create(ctx, &domain.TrackerCredential{
		UserID: uid, TrackerName: trackerName, Username: username,
		SecretEnc: pEnc, SecretNonce: pNonce, SessionEnc: sEnc, SessionNonce: sNonce,
	})
}
```
Update the two call sites (begin no-captcha, complete) to pass the password: begin has `req.Password`; complete has `p.password` from the pending entry.

- [ ] **Step 3: Tests**

Update the existing handler tests for the new `persistSession` signature. Add: complete-success persists a credential whose `SecretEnc` is non-empty (extend `fakeCredStore.Create` to capture the arg; assert `store.created.SecretEnc != nil`).

- [ ] **Step 4: Gate + commit** — `git add backend/internal/api/handlers && git commit -m "feat(api): persist tracker password on interactive add (enables captcha-only re-auth)"`

---

## Task 5: Captcha-only re-auth endpoints

**Files:** `credentials_interactive.go`; `credentials.go` (interface += `MarkSessionExpired`, and `GetByID` already present); `router.go`; `credentials_interactive_test.go`.

- [ ] **Step 1: Handlers**

Add `ReauthBegin` and `ReauthComplete` on `*Credentials`. Use chi URL param `id`.
```go
// ReauthBegin handles POST /credentials/{id}/reauth/begin — re-authenticate
// an existing credential using its STORED password (no re-entry).
func (h *Credentials) ReauthBegin(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r); if perr != nil { problem.Write(w, r, h.BaseURL, perr); return }
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil { problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id")); return }
	cred, cerr := h.Creds.GetByID(r.Context(), id, uid)
	if cerr != nil || cred == nil { problem.Write(w, r, h.BaseURL, problem.ErrNotFound("credential not found")); return }
	if len(cred.SecretEnc) == 0 {
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("this account has no stored password; delete and re-add it to enable captcha-only re-auth")); return
	}
	wi, perr := h.interactivePlugin(cred.TrackerName); if perr != nil { problem.Write(w, r, h.BaseURL, perr); return }
	pw, derr := h.Master.Decrypt(cred.SecretEnc, cred.SecretNonce)
	if derr != nil { problem.Write(w, r, h.BaseURL, problem.ErrInternal("decrypt password: "+derr.Error())); return }
	transient := &domain.TrackerCredential{UserID: uid, TrackerName: cred.TrackerName, Username: cred.Username, SecretEnc: pw}
	challenge, cookies, berr := wi.BeginLogin(r.Context(), transient)
	if berr != nil { problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(berr.Error())); return }
	if cookies != nil {
		if _, serr := h.persistSessionForID(r.Context(), uid, cred, cookies); serr != nil { problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist: "+serr.Error())); return }
		writeJSON(w, http.StatusOK, map[string]any{"status": "logged_in"}); return
	}
	h.pending.put(challenge.ChallengeID, &handlerPending{userID: uid, trackerName: cred.TrackerName, username: cred.Username, credentialID: id})
	writeJSON(w, http.StatusOK, map[string]any{"status": "captcha", "challenge_id": challenge.ChallengeID, "captcha_image": dataURL(challenge.MIMEType, challenge.Image)})
}

// ReauthComplete handles POST /credentials/{id}/reauth/complete.
func (h *Credentials) ReauthComplete(w http.ResponseWriter, r *http.Request) {
	uid, perr := currentUserID(r); if perr != nil { problem.Write(w, r, h.BaseURL, perr); return }
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil { problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid id")); return }
	var req struct{ ChallengeID, Answer string `json:"-"` }
	// decode {challenge_id, answer}
	var body struct{ ChallengeID string `json:"challenge_id"`; Answer string `json:"answer"` }
	if derr := json.NewDecoder(r.Body).Decode(&body); derr != nil { problem.Write(w, r, h.BaseURL, problem.ErrBadRequest("invalid JSON")); return }
	_ = req
	p, ok := h.pending.get(body.ChallengeID, uid)
	if !ok || p.credentialID != id { problem.Write(w, r, h.BaseURL, problem.ErrNotFound("challenge not found or expired")); return }
	wi, perr := h.interactivePlugin(p.trackerName); if perr != nil { problem.Write(w, r, h.BaseURL, perr); return }
	cookies, cerr := wi.CompleteLogin(r.Context(), body.ChallengeID, body.Answer)
	if cerr != nil {
		if errors.Is(cerr, captchalogin.ErrWrongCaptcha) { problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable("captcha_incorrect")); return }
		problem.Write(w, r, h.BaseURL, problem.ErrUnprocessable(cerr.Error())); return
	}
	cookieJSON, _ := json.Marshal(cookies)
	enc, nonce, eerr := h.Master.Encrypt(cookieJSON)
	if eerr != nil { problem.Write(w, r, h.BaseURL, problem.ErrInternal("encrypt: "+eerr.Error())); return }
	if serr := h.Creds.SetSession(r.Context(), id, uid, enc, nonce); serr != nil { problem.Write(w, r, h.BaseURL, problem.ErrInternal("persist: "+serr.Error())); return }
	h.pending.del(body.ChallengeID)
	cred, _ := h.Creds.GetByID(r.Context(), id, uid)
	writeJSON(w, http.StatusOK, map[string]any{"credential": toCredView(cred)})
}
```
Add `credentialID uuid.UUID` to `handlerPending`. Add a small `persistSessionForID(ctx, uid, cred, cookies)` helper (SetSession on cred.ID + return cred) for the no-captcha reauth branch, OR inline it. Clean up the stray `req struct` (that's a placeholder — decode straight into `body`). Add `chi` import.

- [ ] **Step 2: Routes** — in `router.go`, next to the interactive routes:
```go
			r.Post("/credentials/{id}/reauth/begin", credsH.ReauthBegin)
			r.Post("/credentials/{id}/reauth/complete", credsH.ReauthComplete)
```

- [ ] **Step 3: Tests** — handler tests with the fake interactive+credentials store: reauth begin on a credential WITH stored password → captcha (fake `GetByID` returns a cred with non-empty `SecretEnc`); begin on a no-password cred → 422; begin on a foreign cred → 404; complete success → `SetSession` called + 200; wrong answer → 422 `captcha_incorrect`. The `fakeCredStore` needs `GetByID` to return a configurable cred and `SetSession` to record.

- [ ] **Step 4: Gate + commit** — `git add backend/internal/api && git commit -m "feat(api): captcha-only re-auth endpoints for expired sessions"`

---

## Task 6: Expose session_expired to the UI

**Files:** `credentials.go` (`credentialView` + `toCredView`).

- [ ] **Step 1:** Add `SessionExpired bool json:"session_expired"` to `credentialView`; in `toCredView` set it to `c.SessionExpiredAt != nil`.
- [ ] **Step 2: Gate + commit** — `git commit -m "feat(api): expose session_expired in credential view"`

---

## Task 7: Frontend — badge + captcha-only re-auth dialog

**Files:** `frontend/src/lib/api.ts`; `frontend/src/pages/Credentials.tsx`; `i18n/{en,ru}.ts`; `Credentials.test.tsx`. Frontend gate: `docker run --rm -v "E:/Projects/Stukans/Marauder/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`.

- [ ] **Step 1: api.ts** — add `session_expired: boolean` to `CredentialView`; add:
```ts
reauthBegin: (id: string) => request<InteractiveBeginResult>("POST", `/credentials/${id}/reauth/begin`, {}),
reauthComplete: (id: string, body: { challenge_id: string; answer: string }) =>
  request<{ credential: CredentialView }>("POST", `/credentials/${id}/reauth/complete`, body),
```
(reuse the existing `interactiveRefresh` for the image refresh — it's keyed by challenge_id and works for both flows; `InteractiveBeginResult` already exists.)

- [ ] **Step 2: i18n** — add `credentials.sessionExpired` ("Session expired"), `credentials.reauthenticate` ("Re-authenticate"), reuse existing captcha* keys. en + ru.

- [ ] **Step 3: Failing test** — in `Credentials.test.tsx`: a credential row with `session_expired: true` shows the badge + Re-authenticate button; clicking it (mock `reauthBegin` → captcha) shows a captcha-only dialog with NO email/password inputs; submitting (mock `reauthComplete` → success) closes + invalidates the list.

- [ ] **Step 4: Implement** — reuse the existing `CaptchaChallenge` subcomponent. Render the badge + button on each credential row when `c.session_expired`. The button opens a dialog driven by `reauthBegin(id)`→captcha→`reauthComplete(id, {challenge_id, answer})`, with the same wrong-answer→refresh handling and `refresh.isPending` disable logic as the add flow. No credential inputs in this dialog.

- [ ] **Step 5: Frontend gate + commit** — `git add frontend/src && git commit -m "feat(frontend): session-expired badge + captcha-only re-auth dialog"`

---

## Task 8: Docs + close tech debt

**Files:** `CLAUDE.md`; `docs/plugin-development.md`; remove `techdebt/frontend/3-2-...`.

- [ ] **Step 1:** CLAUDE.md — add the `notify` package row; note `session_expired_at`; note the `/credentials/{id}/reauth/*` endpoints and that interactive trackers persist the password.
- [ ] **Step 2:** plugin-development.md — note that interactive trackers store the password to enable captcha-only re-auth, and that session-expiry fires notifications.
- [ ] **Step 3:** `git rm techdebt/frontend/3-2-no-ui-to-reauth-expired-captcha-session.md` (now resolved).
- [ ] **Step 4: Commit** — `git add -A && git commit -m "docs: document re-auth + notify; resolve reauth-UI tech debt"`

---

## Self-Review notes

- Spec coverage: password persist (T4), dispatcher (T2), expiry/dedup/notify (T1+T3), reauth endpoints (T5), session_expired view (T6), frontend (T7), docs (T8). All spec sections mapped.
- Decrypt convention honored: reauth decrypts `secret_enc` → plaintext into a transient cred for `BeginLogin`.
- Dedup correctness: `MarkSessionExpired` sets the marker; `SetSession` clears it; scheduler notifies only when marker is nil.
- Type consistency: `MarkSessionExpired`, `SetSession` (now clears marker), `persistSession(…, password, cookies)`, `handlerPending{password, credentialID}`, `notify.Dispatcher.Send(userID, Message) int`, `credentialView.SessionExpired`.
- Legacy credentials (no `secret_enc`) → 422 with re-add guidance (T5 Step 1).
