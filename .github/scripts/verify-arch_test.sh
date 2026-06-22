#!/usr/bin/env bash
#
# Table-driven tests for verify-arch.sh. No Docker needed — feeds synthetic
# `file -b` descriptions (taken from real static Go binaries) to arch_matches.
#   bash .github/scripts/verify-arch_test.sh
# Exits non-zero if any assertion fails.

set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=verify-arch.sh
source "$DIR/verify-arch.sh"

pass=0
fail=0

# Assert arch_matches' exit status (0 = match, 1 = mismatch).
check_match() {
  local desc="$1" arch="$2" want="$3" label="$4" got
  if arch_matches "$desc" "$arch"; then got=0; else got=1; fi
  if [[ "$got" == "$want" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf 'FAIL: %s\n  arch=%s want-exit=%s got-exit=%s\n  desc=%q\n' \
      "$label" "$arch" "$want" "$got" "$desc"
  fi
}

check_eq() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$expected" == "$actual" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf 'FAIL: %s\n  expected: %q\n  actual:   %q\n' "$label" "$expected" "$actual"
  fi
}

# Real `file -b` output for static, stripped Go binaries:
AMD64_DESC="ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=abc, stripped"
ARM64_DESC="ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked, Go BuildID=abc, stripped"

# ---------------------------------------------------------------- arch_matches
check_match "$AMD64_DESC" amd64 0 "amd64 binary matches amd64"
check_match "$ARM64_DESC" arm64 0 "arm64 binary matches arm64"
check_match "$AMD64_DESC" arm64 1 "amd64 binary does NOT match arm64 (the #74 bug)"
check_match "$ARM64_DESC" amd64 1 "arm64 binary does NOT match amd64"
check_match "garbage not an elf" amd64 1 "non-ELF description never matches"

# ------------------------------------------------------------------ arch_token
check_eq "token amd64" "x86-64"  "$(arch_token amd64)"
check_eq "token arm64" "aarch64" "$(arch_token arm64)"

# Guard: tokens must stay disjoint, else a containment test would mis-match.
check_match "$AMD64_DESC" arm64 1 "amd64 desc must not contain the arm64 token"
check_match "$ARM64_DESC" amd64 1 "arm64 desc must not contain the amd64 token"

# ---------------------------------------------------------------------- tally
if [[ "$fail" -gt 0 ]]; then
  printf '\n%d passed, %d FAILED\n' "$pass" "$fail"
  exit 1
fi
printf '%d passed, 0 failed\n' "$pass"
