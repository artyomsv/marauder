#!/usr/bin/env bash
#
# Tests for bump-version-refs.sh. Copies the REAL repo files into a temp root
# (so fixtures never drift from reality), runs the bump, and asserts every
# mirrored version literal is updated — and that releaseDate is left alone.
#
#   bash .github/scripts/bump-version-refs_test.sh

set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/../.." && pwd)"
SCRIPT="$DIR/bump-version-refs.sh"
VER="9.9.9"

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

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Mirror just the files the script edits, preserving relative paths.
mkdir -p "$TMP/deploy" "$TMP/site/src/data" "$TMP/site/src/pages"
cp "$REPO/deploy/docker-compose.yml"      "$TMP/deploy/"
cp "$REPO/deploy/docker-compose.ghcr.yml" "$TMP/deploy/"
cp "$REPO/deploy/.env.example"            "$TMP/deploy/"
cp "$REPO/site/src/data/seo.ts"           "$TMP/site/src/data/"
cp "$REPO/site/src/pages/install.astro"   "$TMP/site/src/pages/"

# Capture releaseDate before the bump (must be unchanged after).
RELDATE_BEFORE="$(grep -E 'releaseDate:' "$TMP/site/src/data/seo.ts")"

bash "$SCRIPT" "$VER" "$TMP" >/dev/null

count() { grep -cE "$1" "$2"; }

# Every mirrored literal is now 9.9.9 ...
check "compose VERSION arg"          "1" "$(count "VERSION: \"$VER\"" "$TMP/deploy/docker-compose.yml")"
check "compose backend image"        "1" "$(count "image: marauder-backend:$VER" "$TMP/deploy/docker-compose.yml")"
check "ghcr default fallbacks (x3)"  "3" "$(count ":-$VER}" "$TMP/deploy/docker-compose.ghcr.yml")"
check "ghcr comment default"         "1" "$(count "defaults to $VER below" "$TMP/deploy/docker-compose.ghcr.yml")"
check "env.example pin"              "1" "$(count "MARAUDER_VERSION=$VER" "$TMP/deploy/.env.example")"
check "seo.ts software version"      "1" "$(count "version: \"$VER\"" "$TMP/site/src/data/seo.ts")"
check "install.astro example output" "1" "$(count "\"version\": \"$VER\"" "$TMP/site/src/pages/install.astro")"
check "install.astro default note"   "1" "$(count "defaults to <code>$VER</code>" "$TMP/site/src/pages/install.astro")"

# ... and no stale 1.0.x version literals linger in the edited spots.
check "no stale ghcr fallback"       "0" "$(count ":-1\.0\.[0-9]+}" "$TMP/deploy/docker-compose.ghcr.yml")"
check "no stale env pin"             "0" "$(count "MARAUDER_VERSION=1\.0\.[0-9]+" "$TMP/deploy/.env.example")"

# releaseDate (JSON-LD datePublished) must be untouched.
check "releaseDate unchanged" "$RELDATE_BEFORE" "$(grep -E 'releaseDate:' "$TMP/site/src/data/seo.ts")"

# Bad input is rejected.
bash "$SCRIPT" "not-a-version" "$TMP" >/dev/null 2>&1
check "rejects bad version (exit 2)" "2" "$?"

echo "-----------------------------------------"
echo "bump-version-refs: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
