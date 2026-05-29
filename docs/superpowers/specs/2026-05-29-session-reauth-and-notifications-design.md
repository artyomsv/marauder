# Session re-authentication + notification dispatch

Date: 2026-05-29
Status: Design — awaiting review
Builds on: the interactive captcha login feature (merged to main)
Branch: `feature/session-reauth-notify`

## Problem

LostFilm (and future captcha-gated trackers) use a stored `lf_session`
cookie that eventually expires. Today, when it expires the scheduler
returns `registry.ErrSessionExpired` and the topic shows `auth failed`,
but the user has **no notification** and **no way to recover without
deleting and re-adding the credential** (re-entering email + password and
solving a captcha).

Desired: when a session expires, the user is **notified** through their
configured notifiers, and can **re-authenticate by solving only a fresh
captcha** — no email/password re-entry.

## Key discovery shaping this design

**Marauder does not dispatch notifications anywhere yet.** Notifiers are a
CRUD-managed, `Test`-able plugin type, but nothing calls `Notifier.Send`
on an event — the scheduler has no notifier wiring. So this feature must
build the first **notification-dispatch layer**, which also becomes the
foundation for future release notifications.

## Decisions (confirmed)

- Store the tracker password encrypted (existing `crypto.MasterKey` →
  `secret_enc`/`secret_nonce`, exactly like other trackers) so re-auth
  needs only the captcha. (The Java `encryption-common` lib is JVM-only
  and multi-tenant; Marauder is Go + single-tenant and already has the
  equivalent AES-256-GCM primitive — not applicable here.)
- On expiry, dispatch to **all** of the user's configured notifiers
  (no per-event opt-in now; `domain.Notifier.Events` exists for future
  filtering).
- Build all three parts on this branch.

## Goals / Non-goals

Goals:
- A reusable notification-dispatch helper (`userID + domain.Message →
  Send to all that user's notifiers`, best-effort).
- One-shot "session expired, re-authenticate" alert per expiry (deduped).
- Captcha-only re-auth using the stored password — no credential
  re-entry.
- In-app "session expired" badge + re-auth dialog on the credential row.

Non-goals:
- Release/download notifications (the dispatcher is built reusably, but
  wiring those events is a follow-up).
- Per-notifier event subscriptions / routing UI.
- Automatic (unattended) re-auth — a captcha still needs a human.

## Architecture

### Part 1 — Persist the password

The interactive add flow currently persists only the session. Change it
to also store the password (encrypted) so re-auth has it:
- The handler-side pending entry (begin→complete bridge) gains the
  plaintext `password` (in-memory only, 5-min TTL — a deliberate, scoped
  reversal of the earlier "no password in the pending store" decision,
  required for unattended re-auth).
- `persistSession` encrypts BOTH the cookie map (→ `session_enc`) and the
  password (→ `secret_enc`) and writes both on Create. On the upsert
  (re-auth) path it refreshes `session_enc` via `SetSession` and leaves
  `secret_enc` intact (password unchanged).

### Part 2 — Notification dispatch (new)

New package `backend/internal/notify`:
```go
type Dispatcher struct { /* notifiers repo + master key + logger */ }

// Send delivers msg to every notifier configured by userID. Best-effort:
// each notifier is attempted; failures are logged and counted but do not
// abort the others. Returns the count of successful sends.
func (d *Dispatcher) Send(ctx context.Context, userID uuid.UUID, msg domain.Message) int
```
For each `notifiers.ListForUser(userID)`: `registry.GetNotifier(name)` →
`master.Decrypt(ConfigEnc, ConfigNonce)` → `plugin.Send(ctx, raw, msg)`
(mirrors the handler `Test` path). Per-notifier timeout; structured logs;
a Prometheus counter `marauder_notifications_sent_total{notifier,result}`.

The scheduler gains a `notifiersRepo` consumer-side interface seam (like
its existing `topicsRepo`/`clientsRepo`) and holds a `*notify.Dispatcher`.
`scheduler.New` takes the notifiers repo; `cmd/server/main.go` passes it.

### Part 3 — Expiry detection, dedup, and notification

- Migration `0003_add_session_expired_at.sql`: add
  `session_expired_at TIMESTAMPTZ` (nullable) to `tracker_credentials`.
- In `loadCredentials`, when `wc.Login` returns an error matching
  `errors.Is(err, registry.ErrSessionExpired)`:
  - If the credential's `session_expired_at` is NULL → set it
    (`Credentials.MarkSessionExpired(id)`) AND dispatch the notification
    once via `notify.Dispatcher.Send(userID, Message{Title:"Tracker
    session expired", Body:"<tracker> needs re-authentication — solve the
    captcha in Marauder", Link:<public base URL>/credentials})`.
  - If already set → skip (deduped; no repeat notification).
- On successful re-auth (`SetSession`), clear `session_expired_at`
  (`ClearSessionExpired`, folded into `SetSession` or a sibling call) so
  the next expiry notifies again.
- Repo additions: `MarkSessionExpired(id)`, and `SetSession` also clears
  `session_expired_at`. `GetForTracker`/`scanCred` select the new column.

### Part 4 — Captcha-only re-auth endpoints

Two new authenticated endpoints keyed to an EXISTING credential id:
- `POST /credentials/{id}/reauth/begin` — load the credential
  (`GetByID(id, uid)`), decrypt `secret_enc` → plaintext password, build a
  transient `domain.TrackerCredential{Username, SecretEnc: password}`,
  call the plugin's `BeginLogin`. Returns the same `{status:"captcha",
  challenge_id, captcha_image}` / `{status:"logged_in"}` shape as the add
  flow. Pending entry stores the credential id (no password needed —
  re-fetched from DB on complete is unnecessary because the engine holds
  the jar; the handler pending holds `{userID, credentialID, trackerName}`).
- `POST /credentials/{id}/reauth/complete` — `{challenge_id, answer}` →
  plugin `CompleteLogin` → `SetSession(credentialID, ...)` (clears
  `session_expired_at`) → 200 `{credential}`.
- 404 if the credential isn't the caller's; 422 `captcha_incorrect` on a
  wrong answer (pending kept, refresh available via the existing
  `/credentials/interactive/refresh`, which is keyed by challenge id and
  works for both flows).

Plugin requirement: the tracker must implement BOTH `WithCredentials`
(for the stored-password Login rehydration) and `WithInteractiveLogin`
(for the captcha flow). LostFilm already does.

### Part 5 — Frontend

- `credentialView` gains `session_expired bool` (derived from
  `session_expired_at != NULL`). `api.ts` `CredentialView` type updated.
- The credentials list row shows a "Session expired — re-authenticate"
  badge when `session_expired`, with a **Re-authenticate** button that
  opens a **captcha-only** dialog (image + answer + refresh; NO email or
  password fields), calling `/credentials/{id}/reauth/{begin,complete}`
  and the existing refresh endpoint.
- New i18n keys (en + ru). This closes tech-debt `frontend/3-2`.

## Error handling

| Condition | Behavior |
|---|---|
| Session expires (scheduler) | one notification per expiry (deduped via `session_expired_at`); topic shows `auth failed` |
| A notifier `Send` fails | logged + counted; other notifiers still attempted; dispatch never blocks the scheduler |
| Re-auth wrong captcha | 422 `captcha_incorrect`, pending kept, refresh available |
| Re-auth on a credential without a stored password (legacy row) | 422 with a clear "re-add this account" message (pre-feature credentials have no `secret_enc`) |
| Re-auth on another user's credential | 404 |

## Testing

- `notify.Dispatcher` — unit tests with fake notifiers repo + fake
  registered notifier: sends to all; one failing notifier doesn't abort
  others; returns success count; no notifiers → no-op.
- scheduler — `loadCredentials` on `ErrSessionExpired`: notifies + marks
  once; second expired check does NOT re-notify; successful login clears
  the marker. (Consumer-interface fakes, no DB.)
- repo — pgxmock for `MarkSessionExpired`, `SetSession` clearing the
  column, and `session_expired_at` round-trip in `GetByID`/`GetForTracker`.
- handler — reauth begin/complete against a fake interactive+credentials
  plugin: begin returns captcha; complete upserts session + clears
  expiry; legacy-no-password → 422; foreign credential → 404.
- frontend — Vitest: badge shows when `session_expired`; re-auth dialog is
  captcha-only (no credential inputs); complete closes + refreshes list.

Success check: backend `go build ./... && go vet ./... && go test -race
./...` green; frontend `npm run typecheck && npm test && npm run build`
green; manual E2E — force a session to look expired, confirm a
notification fires once and the captcha-only re-auth restores it.

## Documentation

Update CLAUDE.md (new `notify` package; `session_expired_at` column;
reauth endpoints) and `docs/plugin-development.md` (note that interactive
trackers also persist the password to enable captcha-only re-auth).

## Out-of-scope / future

- Release/download notifications reusing `notify.Dispatcher`.
- Per-notifier event subscriptions (`domain.Notifier.Events` filtering).
- A "Test connection" button that reuses the reauth/begin probe.
