# Handler request structs use `*Req`, violating the dto-naming rule

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Medium |
| Location | `backend/internal/api/handlers/*.go` (12 structs across 6 files) |
| Found during | Code review of PR #61 (per-topic notifier override, #51), rules-compliance agent |
| Date | 2026-06-22 |

## Issue

The HTTP handler request bodies are named with a `*Req` suffix, which is not one
of the three approved transport-type names in `~/.claude/rules/dto-naming.md`
(`*Dto`, `*View`, `*Payload`; with `*Request`/`*Response` as the conventional
endpoint-paired names under `*Dto`). `*Req` is an unsanctioned fourth name.

It is a **package-wide convention**, not a one-off — 12 structs across 6 files:

- `auth.go`: `loginReq`, `refreshReq`, `changePasswordReq`
- `clients.go`: `createClientReq`
- `credentials.go`: `createCredentialReq`, `updateCredentialReq`
- `credentials_interactive.go`: `beginReq`, `answerReq`, `refreshChallengeReq`
- `notifiers.go`: `createNotifierReq`
- `topics.go`: `createTopicReq`, `updateTopicReq`

## Why this is tracked, not fixed in PR #61

Renaming only the two `topics.go` structs (the ones PR #61 touched) would make
the package *inconsistent* — worse than leaving it uniform. Renaming all 12 is a
cross-cutting change unrelated to issue #51. The dto-naming rule itself states:
"Renames to align existing code with this rule go in their own commit, never
bundled with feature work." Bundling a 6-file rename into a feature PR would
violate the rule it aims to satisfy and break `git blame` on unrelated lines.

## Risks

Low. Purely a naming-convention drift; no functional, security, or performance
impact. Cost is reviewer friction and a small inconsistency with the documented
standard.

## Suggested Solution

A single dedicated rename commit (no feature changes) converting all 12 handler
request structs `*Req` → `*Request` (e.g. `createTopicReq` → `createTopicRequest`),
updating their references in handlers and `*_test.go` files. Mechanical; one
commit, one PR titled `refactor: rename handler request structs to *Request`.
Optionally also align response structs if any `*Resp`/`*Res` exist.
