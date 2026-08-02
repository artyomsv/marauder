# PUT /credentials/{id} stores an unvalidated password when the plugin is missing

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Small |
| Location | `backend/internal/api/handlers/credentials.go` (Update, the `registry.WithCredentials` type-assertion branch) |
| Found during | Code review of PR #140 (credential verification), code-reviewer agent |
| Date | 2026-08-02 |

## Issue

`Create` and `Update` disagree about what to do when a credential's tracker has
no registered plugin, or has one that does not implement
`registry.WithCredentials`:

- **`Create`** rejects it — `422 unknown tracker plugin` / `tracker does not
  require credentials`. Nothing is persisted.
- **`Update`** silently skips validation. The type assertion fails, the
  `loginAndVerify` block is stepped over entirely, and the new password is
  encrypted and stored with **no login attempt at all**. The response is `200`
  with `verified` absent.

Absent `verified` is indistinguishable from "an update that kept the current
password", which is the other case that legitimately omits the field. So the
API reports the same thing for "nothing needed checking" and "we could not
check and did not try".

## Risks

Low likelihood, and no security exposure — the password is still encrypted at
rest and the row is still owner-scoped.

The exposure is narrow because `Create` blocks the same condition: such a row
can only exist if the plugin set changed *underneath* an existing credential —
a plugin removed in an upgrade, or one that dropped `WithCredentials` in a later
release. That is a real path (Toloka and Unionpeer could in principle drop it)
but not a common one.

The cost when it does happen is a stored password nobody ever validated,
surfacing later as repeated scheduler check failures rather than as an error at
the point the user typed it.

## Suggested Solutions

1. **Mirror `Create`** — return `422` from `Update` for a missing or
   non-`WithCredentials` plugin. Simplest and most consistent, but it is a
   behavioural API change (200 → 422), so it wants its own changelog entry and
   its own revert boundary. That is why it was kept out of PR #140.
2. **Keep storing, report honestly** — persist as today but return an explicit
   marker (e.g. `verified: false`) so the response distinguishes "not checked"
   from "no check needed". Smaller blast radius; leaves the asymmetry in place.

Option 1 is preferred. Not bundled with the credential-verification work
because that PR's thesis is "report unverified logins instead of success",
while this is "validate on update as we do on create" — adjacent invariants,
and per `~/.claude/rules/dto-naming.md` alignment changes belong in their own
commit so they stay visible in review.
