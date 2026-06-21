#!/usr/bin/env bash
#
# Table-driven tests for release-helpers.sh. Runnable locally and in CI:
#   bash .github/scripts/release-helpers_test.sh
# Exits non-zero on the first failing assertion's tally.

set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=release-helpers.sh
source "$DIR/release-helpers.sh"

pass=0
fail=0

check() {
  local desc="$1" expected="$2" actual="$3"
  if [[ "$expected" == "$actual" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf 'FAIL: %s\n  expected: %q\n  actual:   %q\n' "$desc" "$expected" "$actual"
  fi
}

# --------------------------------------------------------------- bump-level
check "feat -> minor"            "minor" "$(bump_level 'feat: add seasons catalog' '')"
check "feat(scope) -> minor"     "minor" "$(bump_level 'feat(topics): per-topic dir' '')"
check "fix -> patch"             "patch" "$(bump_level 'fix(kinozal): resolve infohash' '')"
check "perf -> patch"            "patch" "$(bump_level 'perf: cache decode' '')"
check "feat! -> major"           "major" "$(bump_level 'feat!: drop legacy config' '')"
check "fix(scope)! -> major"     "major" "$(bump_level 'fix(api)!: change payload' '')"
check "body BREAKING -> major"   "major" "$(bump_level 'fix: tweak' 'line

BREAKING CHANGE: removed field')"
check "body BREAKING- -> major"  "major" "$(bump_level 'fix: tweak' 'BREAKING-CHANGE: removed field')"
check "chore -> none"            "none"  "$(bump_level 'chore(deps): bump' '')"
check "chore(deps,fe) -> none"   "none"  "$(bump_level 'chore(deps,frontend)(deps): bump group (#46)' '')"
check "docs -> none"             "none"  "$(bump_level 'docs: update README' '')"
check "ci -> none"               "none"  "$(bump_level 'ci: tweak workflow' '')"
check "refactor -> none"         "none"  "$(bump_level 'refactor(forumcommon): share decode' '')"
check "unknown -> none"          "none"  "$(bump_level 'wip random subject' '')"

# ------------------------------------------------------------- next-version
check "v1.0.2 patch -> 1.0.3"    "1.0.3" "$(next_version 'v1.0.2' 'patch')"
check "v1.0.2 minor -> 1.1.0"    "1.1.0" "$(next_version 'v1.0.2' 'minor')"
check "v1.0.2 major -> 2.0.0"    "2.0.0" "$(next_version 'v1.0.2' 'major')"
check "1.2.3 minor -> 1.3.0"     "1.3.0" "$(next_version '1.2.3' 'minor')"
check "v2.5.9 major -> 3.0.0"    "3.0.0" "$(next_version 'v2.5.9' 'major')"
check "prerelease ignored"       "1.1.0" "$(next_version 'v1.0.2-rc1' 'minor')"
check "empty tag patch -> 0.0.1" "0.0.1" "$(next_version '' 'patch')"
check "empty tag minor -> 0.1.0" "0.1.0" "$(next_version '' 'minor')"

# --------------------------------------------------------------- issue-refs
check "closes keyword"           "12"    "$(issue_refs 'Closes #12' '')"
check "fixes keyword"            "7"     "$(issue_refs 'this fixes #7 nicely' '')"
check "resolved keyword"         "9"     "$(issue_refs 'Resolved #9' '')"
check "branch prefix only"       "48"    "$(issue_refs 'no refs here' '48-kinozal-check-fails')"
check "body + branch deduped"    "$(printf '13\n48')" "$(issue_refs 'Fixes #13' '48-foo')"
check "same number deduped"      "5"     "$(issue_refs 'Closes #5' '5-foo-bar')"
check "no refs -> empty"         ""      "$(issue_refs 'nothing to see' 'feature/no-issue')"
check "multiple in body sorted"  "$(printf '3\n21')" "$(issue_refs 'Closes #21 and fixes #3' '')"

# ------------------------------------------------------- changelog rolling
SAMPLE_CL='# Changelog

## [Unreleased]

### Fixed

- something broke

## [1.0.2] - 2026-06-18

### Added

- a thing'

# Roll renames [Unreleased] to the version and inserts a fresh empty one.
ROLLED="$(printf '%s\n' "$SAMPLE_CL" | roll_changelog '1.1.0' '2026-06-21')"
check "roll keeps fresh Unreleased" "1" "$(printf '%s' "$ROLLED" | grep -c '^## \[Unreleased\]$')"
check "roll adds version heading"   "1" "$(printf '%s' "$ROLLED" | grep -c '^## \[1.1.0\] - 2026-06-21$')"
check "roll preserves old version"  "1" "$(printf '%s' "$ROLLED" | grep -c '^## \[1.0.2\] - 2026-06-18$')"
check "roll keeps existing entry"   "1" "$(printf '%s' "$ROLLED" | grep -c '^- something broke$')"
# The new version heading must come AFTER the fresh Unreleased one.
check "Unreleased before version" "ok" "$(printf '%s' "$ROLLED" | awk '/^## \[Unreleased\]$/{u=NR} /^## \[1.1.0\]/{v=NR} END{print (u<v && u>0)?"ok":"bad"}')"

# unreleased-body extracts only the section content.
check "unreleased body content" "1" "$(printf '%s\n' "$SAMPLE_CL" | unreleased_body | grep -c '^- something broke$')"
check "unreleased body excludes old" "0" "$(printf '%s\n' "$SAMPLE_CL" | unreleased_body | grep -c '^- a thing$')"

# Empty Unreleased -> body is whitespace-only.
EMPTY_CL='# Changelog

## [Unreleased]

## [1.0.2] - 2026-06-18

- old'
check "empty unreleased detected" "" "$(printf '%s\n' "$EMPTY_CL" | unreleased_body | tr -d '[:space:]')"

# Roll with fallback heading+bullet injects under the version heading.
ROLLED_FB="$(printf '%s\n' "$EMPTY_CL" | roll_changelog '1.0.3' '2026-06-21' 'Fixed' 'fix(x): the real PR title')"
check "fallback bullet injected" "1" "$(printf '%s' "$ROLLED_FB" | grep -c '^- fix(x): the real PR title$')"
check "fallback heading injected" "1" "$(printf '%s' "$ROLLED_FB" | grep -c '^### Fixed$')"

# ------------------------------------------------------------------- tally
echo "-----------------------------------------"
echo "release-helpers: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
