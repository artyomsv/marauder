# Interactive (captcha) login for cookie-session trackers

Date: 2026-05-29
Status: Approved design — ready for implementation planning
First consumer: LostFilm.tv

## Problem

LostFilm gates login behind a self-hosted image captcha when the request
comes from an untrusted IP (datacenter/Docker egress). Its `ajaxik`
login returns `{"need_captcha":true,"result":"ok"}` (HTTP 200, no
`error` key). The current `lostfilm` plugin posts `mail`+`pass` on every
scheduler check and cannot pass the captcha, so adding a LostFilm
account fails with a 422 ("verify: session is not logged in").

A prior fix (already in the working tree) makes the plugin detect
`need_captcha` and return `registry.ErrCaptchaRequired` with an
actionable message. This spec covers the real solution: let the user
solve the captcha **inside Marauder**, obtain the `lf_session` cookie
once, persist it, and reuse it for all subsequent checks.

Spike findings that ground this design (verified live against
www.lostfilm.tv and the monitorrent predecessor source):

- Captcha image: `GET /simple_captcha.php` (append `?<rnd>` to refresh) —
  a plain server-rendered image bound to the session cookie. Proxyable.
- Login endpoint: `POST /ajaxik.users.php` with
  `act=users&type=login&mail&pass&rem&need_captcha=1&captcha=<answer>`.
- Answer field name: `captcha`.
- Session cookie to persist: `lf_session`.
- Response classification: `success` → logged in; `error==4` → wrong
  captcha; `need_captcha` → captcha required.
- Sessions appear portable (not hard IP-pinned): monitorrent reused a
  stored `lf_session` across scheduled runs from arbitrary hosts. This
  is the key evidence that a cookie obtained once will keep working for
  checks from the Docker IP.

## Goals / Non-goals

Goals:
- A reusable, tracker-agnostic interactive-login mechanism. LostFilm is
  the first consumer; adding a second captcha tracker must require only
  per-tracker config (URLs, form builder, response classifier), no new
  infrastructure.
- Persist the obtained session cookie(s) encrypted at rest in a
  dedicated, first-class column (not a plugin-specific blob).
- Survive backend restarts without re-prompting for a captcha.
- Clear, actionable errors when a stored session expires.

Non-goals:
- Automated captcha solving (OCR / paid services / headless browser).
  This is purely human-in-the-loop relay.
- Routing tracker traffic through proxies / cfsolver. Out of scope.
- Migrating other trackers to cookie-session auth now (the design just
  makes it easy later).

## Architecture

Layering (verified cycle-free: `registry` imports only `domain`;
`forumcommon` imports nothing internal):

```
registry/                 contract: LoginChallenge, SessionCookies,
                          WithInteractiveLogin
plugins/captchalogin/     NEW reusable engine + pending store
                          (imports registry + forumcommon)
forumcommon/              unchanged + CookiesByName(jar, names) helper
plugins/trackers/lostfilm wires a captchalogin.Config (~30 LOC)
api/handlers/credentials  two new endpoints + handler-side pending map
frontend Credentials.tsx  captcha step, gated by capability flag
```

### Data model

New migration `0002_add_session_columns.sql` (goose Up/Down):

```sql
-- +goose Up
ALTER TABLE tracker_credentials ADD COLUMN session_enc   BYTEA;
ALTER TABLE tracker_credentials ADD COLUMN session_nonce BYTEA;
-- +goose Down
ALTER TABLE tracker_credentials DROP COLUMN session_enc;
ALTER TABLE tracker_credentials DROP COLUMN session_nonce;
```

`domain.TrackerCredential` gains `SessionEnc []byte` and
`SessionNonce []byte`.

The stored plaintext (before encryption) is a JSON cookie map so it
generalises to multi-cookie sessions:

```json
{"lf_session": "<value>"}
```

Encrypt with the existing `crypto.Master.Encrypt` → `session_enc` +
`session_nonce`. `username` keeps the email; `secret_enc` keeps the
password (used only to pre-fill a future re-solve — it cannot
auto-recover because a captcha is mandatory).

### Registry contract (new types in `registry`)

```go
type LoginChallenge struct {
    ChallengeID string // correlates Begin -> Complete
    ImagePNG    []byte
    MIMEType    string // e.g. "image/png"
}

type SessionCookies map[string]string // cookie name -> value

// WithInteractiveLogin is an optional capability for trackers that gate
// login behind a captcha the user must solve. Exactly one of
// (challenge, cookies) returned by BeginLogin is non-nil.
type WithInteractiveLogin interface {
    Tracker
    BeginLogin(ctx context.Context, creds *domain.TrackerCredential) (*LoginChallenge, SessionCookies, error)
    CompleteLogin(ctx context.Context, challengeID, answer string) (SessionCookies, error)
}
```

Also add sentinel `registry.ErrSessionExpired` (a stored session failed
validation; the user must re-run interactive login).

### Reusable engine (`plugins/captchalogin`)

```go
type Outcome int
const (
    OutcomeSuccess Outcome = iota
    OutcomeNeedCaptcha
    OutcomeWrongCaptcha
    OutcomeFailed
)

type Config struct {
    LoginURL   string
    CaptchaURL string
    CookieNames []string // cookies to harvest into SessionCookies
    BuildForm  func(c *domain.TrackerCredential, answer string, needCaptcha bool) url.Values
    Classify   func(body []byte) Outcome
}

type Engine struct { /* cfg, pending store, session factory */ }

func (e *Engine) Begin(ctx, creds) (*registry.LoginChallenge, registry.SessionCookies, error)
func (e *Engine) Complete(ctx, challengeID, answer string) (registry.SessionCookies, error)
```

`Classify` is the only genuinely tracker-specific bit; the engine never
grows per-tracker branches. The engine owns a `pendingStore`: a
TTL'd (~5 min) map `challengeID -> {jar *forumcommon.Session, creds}`
with lazy expiry on access plus periodic eviction. `challengeID` is
crypto-random hex.

Engine.Begin:
1. New session jar; POST `LoginURL` with `BuildForm(creds, "", false)`.
2. `Classify(body)`:
   - `OutcomeSuccess` → harvest `CookieNames` from jar → return
     `(nil, cookies, nil)`.
   - `OutcomeNeedCaptcha` → `GET CaptchaURL` on the same jar → read image
     → store pending under a new `challengeID` → return
     `(&LoginChallenge{...}, nil, nil)`.
   - else → error.

Engine.Complete:
1. Load pending by `challengeID` (error if missing/expired).
2. POST `LoginURL` with `BuildForm(creds, answer, true)` on the stored jar.
3. `Classify(body)`:
   - `OutcomeSuccess` → harvest cookies → evict pending → return cookies.
   - `OutcomeWrongCaptcha` → return typed error (caller restarts via
     a fresh Begin); evict pending.
   - else → error; evict pending.

### LostFilm wiring

`captchalogin.Config`:
- `LoginURL`  = `https://<domain>/ajaxik.users.php`
- `CaptchaURL`= `https://<domain>/simple_captcha.php`
- `CookieNames` = `["lf_session"]`
- `BuildForm` → `act=users,type=login,mail=Username,pass=SecretEnc,
  rem=1,need_captcha=<0|1>,captcha=<answer>`
- `Classify` → parse JSON: `success` truthy → Success; `error==4` →
  WrongCaptcha; `need_captcha` truthy → NeedCaptcha; else Failed.

`plugin` implements `WithInteractiveLogin` by delegating to the engine.

Rewritten `Login` (scheduler path, `WithCredentials`):
1. If `SessionEnc` empty → return error wrapping
   `registry.ErrSessionExpired` ("no session; add the account via the
   captcha flow").
2. Decrypt → JSON cookie map → load cookies into the `(tracker,userID)`
   jar.
3. Validate via existing `Verify` (one `GET /my`). If invalid → return
   error wrapping `registry.ErrSessionExpired`.
4. Else mark `LoggedIn` and return nil.

Rationale: the scheduler calls `Login` per check but not `Verify`
separately (`scheduler.go:384`). Folding a single `Verify` into `Login`
turns an expired cookie into a clear `auth failed: ... session expired`
topic error instead of an opaque downstream parse failure. Checks run on
the per-topic interval (default 15m) so the extra GET is negligible.

The transport-rewrite hook (`p.transport`) used by tests must be applied
to engine-created jars too, so the e2e host-rewriter keeps working.

### API endpoints (`api/handlers/credentials.go`)

Both admin-authenticated, same as existing `/credentials`.

`POST /api/v1/credentials/interactive/begin`
- body: `{tracker_name, username, password}`
- 404 if no plugin; 422 if plugin lacks `WithInteractiveLogin`.
- Build a transient `*domain.TrackerCredential{UserID, TrackerName,
  Username, SecretEnc:[]byte(password)}` and call `BeginLogin`.
- cookies returned (no captcha) → encrypt cookie map, persist credential,
  return `{status:"logged_in", credential: <CredView>}`.
- challenge returned → store handler-side pending
  `challengeID -> {userID, tracker, username}` (TTL ~5 min; NOT the
  password) and return
  `{status:"captcha", challenge_id, captcha_image:"data:<mime>;base64,<...>"}`.

`POST /api/v1/credentials/interactive/complete`
- body: `{tracker_name, challenge_id, answer}`
- Look up handler-side pending; verify it belongs to the caller's
  `userID` (else 404). Call `CompleteLogin`.
- success → encrypt cookie map, persist credential (username from the
  pending entry), evict pending, audit `credential_create`, return
  `{credential: <CredView>}` (201).
- wrong captcha → 422 "captcha incorrect; please restart"; frontend
  re-calls begin for a fresh image.

Handler-side pending store mirrors the engine's: small mutex-guarded map
with TTL. It holds only non-secret correlation metadata. The password
lives solely in the engine's pending jar entry for the seconds between
begin and complete, and is never echoed to the browser.

### Capability discovery

`trackerMatch` (GET `/api/v1/trackers/match`) and the credentials form's
tracker lookup gain `supports_interactive_login bool`, true when the
plugin implements `registry.WithInteractiveLogin`.

### Frontend (`frontend/src/pages/Credentials.tsx`)

When the selected tracker has `supports_interactive_login`:
1. User enters email + password, clicks "Login & save".
2. POST `/interactive/begin`.
   - `logged_in` → toast success, invalidate `QK.credentials`, close.
   - `captcha` → render `<img src={captcha_image}>`, an answer input,
     and a "↻ refresh" link that re-calls begin.
3. User submits answer → POST `/interactive/complete`.
   - success → credential row appears, close.
   - 422 → inline error + auto-refresh a new captcha image.

Non-interactive trackers keep the existing single-step create flow
unchanged. Add new `QK` keys, React Query mutations, and en/ru `useT()`
strings. Captcha image rendered from the returned data URL (no direct
calls from the browser to the tracker — session stays server-side).

## Error handling

| Condition | Surfaced as |
|---|---|
| Tracker has no `WithInteractiveLogin` | 422 at `/begin` |
| Wrong captcha answer | 422 at `/complete`; UI refreshes image |
| Challenge expired / unknown | 404 at `/complete`; UI restarts flow |
| Challenge not owned by caller | 404 (no info leak) |
| Stored session expired (scheduler) | `Login` returns `ErrSessionExpired`; topic shows `auth failed: ... session expired`; user re-runs flow |
| LostFilm unreachable mid-flow | 502/error surfaced from begin/complete |

## Testing

- `captchalogin` engine — httptest stub: success-no-captcha,
  need-captcha→complete-success, wrong-captcha, TTL eviction, unknown
  challenge ID, cookie harvesting.
- LostFilm — extend fixture e2e: stub `/ajaxik.users.php` +
  `/simple_captcha.php`; assert `BeginLogin` returns image bytes,
  `CompleteLogin` harvests `lf_session`, rewritten `Login` rehydrates
  from `session_enc` and validates, and returns `ErrSessionExpired` on a
  dead cookie.
- repo — pgxmock test for `SetSession` and round-trip of the new columns.
- handler — table test against a fake `WithInteractiveLogin`: begin
  (both branches), complete success, wrong-answer 422, caller-ownership
  enforcement.
- frontend — Vitest/RTL: capability-gated rendering, image shown,
  complete-success closes form, 422 shows inline error + refresh.

Success check: `docker run ... golang:1.23 sh -c "go build ./... &&
go vet ./... && go test -race ./..."` green, frontend
`npm run typecheck && npm test && npm run build` green, and a manual
end-to-end add of a LostFilm account via the captcha flow persists a
working `lf_session` (verified by a subsequent successful Check).

## Documentation

Update `CLAUDE.md` (new `captchalogin` package row; note the
`session_enc`/`session_nonce` columns) and `docs/plugin-development.md`
(how to add interactive login to a plugin) in the same commit as the
implementation.

## Out-of-scope / future

- cfsolver-based automated solving (if a future tracker uses a JS
  captcha rather than a relayable image).
- A "Test / re-authenticate" button on existing credential rows reusing
  the same begin/complete flow (natural follow-up; not required now).
