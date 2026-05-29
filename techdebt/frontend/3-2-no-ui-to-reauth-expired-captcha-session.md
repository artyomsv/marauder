# No UI path to re-authenticate an expired captcha session

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Small |
| Location | `frontend/src/pages/Credentials.tsx` (AddCredentialCard tracker filtering, ~`available = trackers.filter(t => !taken.has(t.name))`) |
| Found during | Final pre-merge review of the interactive-captcha-login feature |
| Date | 2026-05-29 |

## Issue

The interactive (captcha) login feature lets a user obtain and persist a
LostFilm `lf_session` cookie. When that session expires, the scheduler's
`Login` returns `registry.ErrSessionExpired` and the design intent is that
the user re-runs the captcha flow to refresh it.

The **backend now supports re-auth**: `persistSession` upserts via
`SetSession` (commit `7f3edd9`), so calling
`POST /credentials/interactive/{begin,complete}` again for an
already-stored account refreshes the session in place — no duplicate-key
500.

But there is **no UI path to trigger it**: the add-credential form filters
out any tracker that already has a credential
(`available = trackers.filter(t => !taken.has(t.name))`), so once a
LostFilm credential row exists, LostFilm disappears from the "Add tracker
account" dropdown. The user cannot re-run the captcha flow from the UI.

The spec (`docs/superpowers/specs/2026-05-29-lostfilm-interactive-captcha-login-design.md`,
"Out-of-scope / future") explicitly deferred a "Test / re-authenticate"
button on existing credential rows, so this is a known, accepted gap for
the initial merge — recorded here so it isn't lost.

## Risks

- A LostFilm account silently stops working when its session expires;
  topics show `auth failed: ... session expired` and the user has no
  obvious in-app way to fix it short of deleting and re-adding the
  credential (delete + re-add does work today, since the tracker
  reappears in the dropdown once untaken — but that's non-obvious).
- Undermines the main value of the cookie-session design (surviving
  restarts / long-lived sessions) because recovery is awkward.

## Suggested Solutions

1. **Re-authenticate button on the credential row** (matches the spec's
   future item): add a button on each interactive-login credential in the
   credentials list that opens the captcha flow for that tracker, calling
   the existing `/interactive/begin`→`/complete` endpoints. Smallest,
   clearest UX.
2. **Surface session health**: show a "session expired — re-authenticate"
   state on the credential row (the backend already distinguishes
   `ErrSessionExpired`; could be exposed via the credential view or a
   `/credentials/{id}/test` call) and link it to solution 1.
3. **Allow re-selecting interactive trackers in the Add form**: don't
   filter out interactive-login trackers that already have a credential,
   since the backend upserts. Cheapest but conflates "add" with "re-auth".
