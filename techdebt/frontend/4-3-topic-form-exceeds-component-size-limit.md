# TopicForm.tsx exceeds the 250-line component-size limit

| Field | Value |
|-------|-------|
| Criticality | Low |
| Complexity | Medium |
| Location | `frontend/src/components/topics/TopicForm.tsx` (~412 lines) |
| Found during | Code review of issue #91 (qBittorrent category dropdown), rules-compliance agent |
| Date | 2026-06-27 |

## Issue

`clean-code-react.md` caps components at 250 lines. `TopicForm.tsx` is ~412
lines — it was already ~400 before the #91 category-combobox work (which added
~12 lines for the `effectiveClientId` resolution + categories query). It is the
shared add/edit form and owns several cohesive but separable blocks:

- the delivery fields (client picker + `NotifierSelect` + download-dir input +
  `CategoryField`)
- the credential-hint banners (requires / optional / satisfied)
- the quality select and the preview card

It is a sibling of the already-acknowledged `Topics.tsx` / `Clients.tsx`
pre-existing size debt noted in `CLAUDE.md`.

## Risks

Low. Purely maintainability — a long file is harder to scan and review. No
functional, security, or performance impact. The form is well covered by the
`AddTopicCard` test suite.

## Suggested Solutions

Extract presentational sub-components in a dedicated refactor commit (not bundled
with feature work, so `git blame` stays meaningful):

1. `TopicDeliveryFields.tsx` — client/notifier/download-dir/category block (~60
   lines). This is where the #91 category combobox lives, so it is the most
   natural first extraction.
2. `TopicCredentialHint.tsx` — the three credential banners (~30 lines), a pure
   function of `match` + `hasCredential`.

Together these bring the file comfortably under 250 lines without touching form
state or behaviour. The `AddTopicCard` tests assert behaviour by label/role, so
a verbatim JSX move keeps them green.
