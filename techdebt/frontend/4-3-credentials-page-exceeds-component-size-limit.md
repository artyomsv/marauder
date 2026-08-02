# Credentials.tsx exceeds the 250-line component-size limit

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Medium |
| Location | `frontend/src/pages/Credentials.tsx` (~816 lines) |
| Found during | Code review of the credential-verification work (PR #140), rules-compliance agent |
| Date | 2026-08-02 |

## Issue

`clean-code-react.md` caps components at 250 lines. `Credentials.tsx` is ~816
lines — over 3x the cap, and the largest breach in the frontend. It was already
~797 lines before PR #140, which added ~19 net lines (the `unverified` banner
state, the `testPhase` derivation, and the two new component imports).

The PR pushed *against* the trend rather than with it — it extracted
`TestLoginIcon` (30 lines) and `VerificationNotice` (38 lines) into
`components/credentials/` instead of inlining more JSX, and replaced a
four-branch nested ternary with a single component call. But the file was
already far over the limit and is still growing.

The file hosts four separate function components that are natural extraction
candidates, all already self-contained in the same file:

- `AddCredentialCard` — the add form, including the whole interactive-captcha
  begin/complete/refresh flow
- `EditCredentialCard` — username/password rotation
- `ReauthDialog` — the captcha-only re-auth flow for an expired session
- `CredentialsPage` — the list, the row actions, and the test-login mutation

It is a sibling of the already-acknowledged `Clients.tsx` and `TopicForm.tsx`
size debt noted in `CLAUDE.md`.

## Risks

Low. Purely maintainability — a long file is harder to scan, review and diff,
and it makes further growth feel cheap. No functional, security, or performance
impact.

One concrete review cost was visible in PR #140: the page's test-login wiring
(the `testPhase` derivation and the page-level `unverified` banner) has no test
coverage, because there is no natural seam to render in isolation — the page
pulls React Query, the auth store, `useSystemInfo` and four sub-components. The
two extracted leaf components *are* tested. Extraction and testability are the
same problem here.

## Suggested Solutions

Extract in a dedicated refactor commit (not bundled with feature work, so
`git blame` stays meaningful). Ordered by payoff:

1. `components/credentials/AddCredentialCard.tsx` — the largest block by far and
   the one carrying the interactive-login state machine. Biggest single win.
2. `components/credentials/ReauthDialog.tsx` — self-contained, already takes a
   `credential` prop and two callbacks; a near-verbatim move.
3. `components/credentials/EditCredentialCard.tsx` — same shape as above.

That leaves `CredentialsPage` as a list + row actions, comfortably under the
cap, and gives the test-login wiring a seam that can be rendered and asserted
without standing up the whole page.
