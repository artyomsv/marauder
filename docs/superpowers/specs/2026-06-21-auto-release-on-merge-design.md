# Auto-release on PR merge — design

**Date:** 2026-06-21
**Status:** approved (proceeding to implementation)
**Branch:** `ci/auto-release-on-merge`

## Problem

Releasing Marauder is manual and error-prone:

1. A human hand-edits `CHANGELOG.md`, moving the `[Unreleased]` block to a
   `[X.Y.Z]` section.
2. A human picks the next version and pushes a `vX.Y.Z` tag.
3. `release.yml` (tag trigger) builds/signs/SBOMs the three images and creates
   the GitHub Release from the matching `CHANGELOG.md` section.
4. `bump-dev-version` syncs the version into `deploy/docker-compose.yml`.

Steps 1–2 are the pain: choosing the version by hand is annoying and forgetting
the changelog roll produces empty release notes.

## Goal

When a PR is squash-merged to `main`, automatically:

- Derive the next semantic version from the merged PR's Conventional Commit
  title (squash subject), so no human picks a number.
- Roll `CHANGELOG.md` (`[Unreleased]` → `[X.Y.Z]`), create + push the tag, and
  let the existing `release.yml` build and publish as it does today.
- Compose the GitHub Release notes from **both** the `CHANGELOG.md` section and
  the merged PR's description.
- If the PR is linked to a GitHub issue (closing keyword **or** issue-number
  branch prefix), comment on that issue that the fix/feature shipped and can be
  tested with the released image tag.

## Decisions (locked with the user)

| Decision | Choice |
|---|---|
| Which merges cut a release | **Only version-bumping Conventional Commit types.** `feat`→minor, `fix`/`perf`→patch, `feat!`/`BREAKING CHANGE`→major. `chore`/`docs`/`ci`/`test`/`refactor`/`build` (incl. dependabot dep bumps) merge silently with **no** release. |
| Bump source | **PR title** (squash-merge makes it the commit subject); PR body scanned for `BREAKING CHANGE`. |
| Issue discovery | **Both** PR-body closing keywords (`Closes/Fixes/Resolves #N`) **and** leading issue-number branch prefix (e.g. `48-kinozal-...`). |
| Empty `[Unreleased]` on a version PR | Synthesize one bullet from the **real PR title** (never fabricated data — it's the actual change description). |
| `docker-compose.ghcr.yml` default tag | Out of scope. Only the existing `bump-dev-version` (source stack) stays. |

## Architecture — the annotated tag is the carrier

```
PR merged to main
      │
      ▼
┌──────────────────────────┐   pushes annotated   ┌───────────────────────┐
│  auto-release.yml (NEW)   │ ───── tag vX.Y.Z ───▶│  release.yml (extend)  │
│  • parse bump from title  │  (msg = PR title +   │  • build/sign/SBOM     │
│  • compute next version   │   PR body +          │  • notes = CHANGELOG   │
│  • roll CHANGELOG.md       │   "Issues:" trailer) │    section + PR body   │
│  • create + push ann. tag │                      │  • notify-issues job   │
└──────────────────────────┘                      └───────────────────────┘
```

### Required one-time setup — `RELEASE_PAT` secret

GitHub suppresses workflow re-triggering for events raised by the built-in
`GITHUB_TOKEN` (recursion guard), so a tag pushed with it would **not** start
`release.yml`. `auto-release.yml` therefore pushes the tag with a
`RELEASE_PAT` secret (a fine-grained PAT with `contents: write`) when present.
Without it the workflow still rolls the CHANGELOG and creates the tag, but
`release.yml` must be started manually via its `workflow_dispatch` — a warning
annotation is emitted in that case. This keeps the proven tag-push path in
`release.yml` 100% untouched (no reusable-workflow refactor, no changes to the
image build/tagging logic).

**Why route through an annotated git tag** instead of refactoring `release.yml`
into a reusable `workflow_call`: `release.yml` already fires on `v*` tag push
and is the single authority that builds images and creates the GitHub Release.
Embedding the PR body + issue numbers in the **annotated tag message** lets
`release.yml` read them with `git tag -l --format='%(contents)' "$TAG"` without
a second trigger path (which would risk a double release) and — crucially —
means the issue comment fires **after** artifacts exist, not seconds after the
tag is created.

## Components

### 1. `.github/scripts/release-helpers.sh` (new, unit-tested)

Pure string logic, extracted from YAML so the risky parsing is testable:

- `bump-level "<pr-title>" "<pr-body>"` → `major|minor|patch|none`
  - `feat!` / `BREAKING CHANGE:` → `major`
  - `feat` → `minor`
  - `fix` / `perf` → `patch`
  - anything else → `none`
- `next-version <latest-tag> <level>` → `X.Y.Z`
  - resets lower components on a higher bump (minor zeroes patch, major zeroes
    minor+patch).
- `issue-refs "<pr-body>" "<branch>"` → newline list of issue numbers, deduped,
  from `Closes/Fixes/Resolves #N` (case-insensitive) and a leading `^[0-9]+`
  branch prefix.

Companion `release-helpers_test.sh` runs table-driven assertions and exits
non-zero on any mismatch.

### 2. `.github/workflows/auto-release.yml` (new)

- Trigger: `pull_request: { types: [closed], branches: [main] }`.
- Guard: `if: github.event.pull_request.merged == true`.
- `concurrency: { group: auto-release, cancel-in-progress: false }` —
  serializes near-simultaneous merges so two PRs can't compute the same next
  version off the same latest tag.
- `permissions: { contents: write }`.
- Steps:
  1. Checkout `main`, full history + tags.
  2. `LEVEL=$(release-helpers.sh bump-level "$TITLE" "$BODY")`. If `none` →
     log and exit 0 (no release).
  3. `LATEST=$(git tag --sort=-v:refname | head -1)`;
     `VER=$(release-helpers.sh next-version "$LATEST" "$LEVEL")`.
  4. If tag `v$VER` already exists → log and exit 0 (idempotent re-run).
  5. Roll `CHANGELOG.md` (see Component 3). Commit to `main` with `[skip ci]`.
  6. Collect issue refs; build the annotated-tag message:
     ```
     <PR title>

     <PR body>

     Issues: #<a> #<b>
     PR: #<num>
     ```
  7. `git tag -a "v$VER" -F <msgfile>` then `git push origin "v$VER"`.

### 3. CHANGELOG roll (inside auto-release.yml)

`awk`/`sed` transform: rename the `## [Unreleased]` heading to
`## [X.Y.Z] - YYYY-MM-DD` and insert a fresh empty `## [Unreleased]` block
above it. If the `[Unreleased]` body is empty, insert a single bullet under an
`### Added` (feat) / `### Fixed` (fix) heading derived from the PR title so the
release notes are never blank. The build date uses the workflow run date (UTC).

### 4. `release.yml` extensions

- **Release-notes step**: after the existing CHANGELOG extraction, read the tag
  annotation and append a `## Pull Request` section containing the PR body.
  Defensive: a manual `workflow_dispatch` run or a hand-pushed tag with no
  annotation simply skips the PR section — existing behavior preserved.
- **New `notify-issues` job** (`needs: [release]`,
  `permissions: { issues: write }`): parse the `Issues:` trailer from the tag
  annotation and `gh issue comment` each one:
  > 🚀 Released in **vX.Y.Z**. Pull `ghcr.io/artyomsv/marauder-backend:X.Y.Z`
  > (and `-frontend` / `-cfsolver`) to test, or update your stack's
  > `MARAUDER_VERSION`. Please verify and report back here.
  No annotation / no issues → job no-ops. Comment failure is non-fatal.

## Error handling

| Case | Behavior |
|---|---|
| Non-version PR (`chore`, etc.) | Graceful skip, exit 0, no tag. |
| Empty `[Unreleased]` on a version PR | Synthesize one bullet from the PR title. |
| Tag already exists (re-run) | Detect and skip. |
| Concurrent merges | `concurrency` group serializes; second run recomputes off the now-latest tag. |
| Issue comment fails | Non-fatal — release is already published. |
| Manual `workflow_dispatch` release | No annotation → no PR section, no issue comments (unchanged). |

## Testing / success criteria

1. **Unit:** `release-helpers_test.sh` passes. Cases include:
   `feat: x`→minor, `fix(scope): y`→patch, `feat!: z`→major,
   `fix: a\n\nBREAKING CHANGE: b`→major, `chore(deps): c`→none;
   `next-version v1.0.2 minor`→`1.1.0`, `v1.0.2 major`→`2.0.0`,
   `v1.0.2 patch`→`1.0.3`; issue-refs from body + `48-foo` branch → `48`.
   Wired into a small CI job so regressions are caught pre-merge.
2. **End-to-end (user acceptance):** merge a `feat:`/`fix:` PR linked to an
   issue → a `vX.Y.Z` tag appears, the GitHub Release body contains both the
   CHANGELOG section and the PR description, and the linked issue receives the
   release comment.

## Out of scope

- Replacing the manual Keep-a-Changelog format with a generator (release-please
  etc.) — the design intentionally consumes the existing `[Unreleased]` block.
- Bumping the `docker-compose.ghcr.yml` default `MARAUDER_VERSION` fallback.
