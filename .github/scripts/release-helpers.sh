#!/usr/bin/env bash
#
# release-helpers.sh — pure-function helpers for the auto-release pipeline.
#
# Extracted from workflow YAML so the risky parsing (Conventional Commit ->
# semver bump, next-version arithmetic, issue-number extraction) is unit
# testable. See release-helpers_test.sh.
#
# Subcommands:
#   bump-level   "<pr-title>" "<pr-body>"   -> major|minor|patch|none
#   next-version <latest-tag>  <level>      -> X.Y.Z
#   issue-refs   "<pr-body>"   "<branch>"   -> newline-separated issue numbers
#   unreleased-body                         -> CHANGELOG [Unreleased] body (stdin)
#   roll-changelog <ver> <date> [h] [bul]   -> rolled CHANGELOG (stdin -> stdout)
#
# All output goes to stdout; the script is side-effect free (roll-changelog and
# unreleased-body read CHANGELOG from stdin and never touch the filesystem).

set -euo pipefail

# ---------------------------------------------------------------- bump-level
#
# Maps a merged PR's Conventional Commit title (the squash subject) to a semver
# bump level. Only version-bumping types produce a release:
#   feat!  / BREAKING CHANGE  -> major
#   feat                      -> minor
#   fix / perf                -> patch
#   everything else           -> none   (chore/docs/ci/test/refactor/build/...)
bump_level() {
  local title="$1" body="${2:-}"

  # Breaking change wins regardless of type:
  #   - a "!" before the colon in the title  (feat!: / fix(scope)!: ...)
  #   - a "BREAKING CHANGE:" / "BREAKING-CHANGE:" line anywhere in the body
  if [[ "$title" =~ ^[a-zA-Z]+(\([^\)]*\))?! ]]; then
    echo "major"; return
  fi
  if printf '%s' "$body" | grep -qiE '(^|[[:space:]])BREAKING[ -]CHANGE:'; then
    echo "major"; return
  fi

  # Extract the leading type token (letters before an optional "(scope)" / "!"
  # / ":"). Lowercased for a case-insensitive match.
  local type
  type=$(printf '%s' "$title" | sed -E 's/^([a-zA-Z]+).*/\1/' | tr '[:upper:]' '[:lower:]')

  case "$type" in
    feat)       echo "minor" ;;
    fix|perf)   echo "patch" ;;
    *)          echo "none"  ;;
  esac
}

# -------------------------------------------------------------- next-version
#
# Given the latest "vX.Y.Z" tag (or empty for a first release) and a bump
# level, prints the next version WITHOUT the leading "v". A higher bump zeroes
# the lower components. Any pre-release suffix on the latest tag is ignored.
next_version() {
  local latest="$1" level="$2"

  # Strip leading "v" and any "-prerelease"/"+build" suffix, then default
  # missing components to 0.
  local core="${latest#v}"
  core="${core%%-*}"
  core="${core%%+*}"
  [[ -z "$core" ]] && core="0.0.0"

  local major minor patch
  major="${core%%.*}";              major="${major:-0}"
  local rest="${core#*.}"
  minor="${rest%%.*}";              minor="${minor:-0}"
  patch="${rest#*.}";               patch="${patch:-0}"
  # Guard against malformed tags leaving non-numerics.
  [[ "$major" =~ ^[0-9]+$ ]] || major=0
  [[ "$minor" =~ ^[0-9]+$ ]] || minor=0
  [[ "$patch" =~ ^[0-9]+$ ]] || patch=0

  case "$level" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
    *) echo "next-version: unknown level '$level'" >&2; return 1 ;;
  esac

  echo "${major}.${minor}.${patch}"
}

# ---------------------------------------------------------------- issue-refs
#
# Extracts GitHub issue numbers a merged PR should notify, from two sources:
#   1. Closing keywords in the PR body: Close(s|d) / Fix(es|ed) / Resolve(s|d)
#      followed by "#<n>" (case-insensitive, GitHub's own keyword set).
#   2. A leading issue-number branch prefix, e.g. "48-kinozal-...".
# Output is newline-separated, numerically sorted, de-duplicated.
issue_refs() {
  local body="${1:-}" branch="${2:-}"
  {
    # Closing keywords -> #<n>. grep -o the whole match, then keep digits.
    printf '%s' "$body" \
      | grep -ioE '(close[sd]?|fix(e[sd])?|resolve[sd]?)[[:space:]]+#[0-9]+' \
      | grep -oE '[0-9]+' || true

    # Leading numeric branch prefix.
    if [[ "$branch" =~ ^([0-9]+) ]]; then
      echo "${BASH_REMATCH[1]}"
    fi
  } | sort -n -u
}

# ----------------------------------------------------------- unreleased-body
#
# Prints the body lines of the "## [Unreleased]" section (everything between
# that heading and the next "## " heading), read from CHANGELOG on stdin. Used
# to decide whether the developer forgot to fill in the changelog.
unreleased_body() {
  awk '
    $0 == "## [Unreleased]" {flag=1; next}
    /^## / && flag {flag=0}
    flag {print}
  '
}

# ----------------------------------------------------------- roll-changelog
#
# Reads CHANGELOG.md on stdin and prints a rolled copy on stdout:
#   - "## [Unreleased]"  becomes  "## [<ver>] - <date>"  (its content moves
#     under the released version), and a fresh empty "## [Unreleased]" block is
#     inserted above it.
#   - If a heading + bullet are given (passed by the caller only when the
#     Unreleased body was empty), they are injected under the new version
#     heading so the release notes are never blank.
roll_changelog() {
  local ver="$1" date="$2" heading="${3:-}" bullet="${4:-}"
  awk -v ver="$ver" -v date="$date" -v heading="$heading" -v bullet="$bullet" '
    $0 == "## [Unreleased]" && !done {
      print "## [Unreleased]"
      print ""
      print "## [" ver "] - " date
      if (heading != "") {
        print ""
        print "### " heading
        print ""
        print "- " bullet
      }
      done=1
      next
    }
    { print }
  '
}

# ---------------------------------------------------------------- dispatch
main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
    bump-level)     bump_level     "${1:-}" "${2:-}" ;;
    next-version)   next_version   "${1:-}" "${2:-}" ;;
    issue-refs)     issue_refs     "${1:-}" "${2:-}" ;;
    unreleased-body) unreleased_body ;;
    roll-changelog) roll_changelog "${1:-}" "${2:-}" "${3:-}" "${4:-}" ;;
    *)
      echo "usage: $0 {bump-level|next-version|issue-refs|unreleased-body|roll-changelog} ..." >&2
      return 2
      ;;
  esac
}

# Only run main when executed directly (so the test can source the functions).
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
