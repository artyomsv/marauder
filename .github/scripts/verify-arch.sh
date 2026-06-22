#!/usr/bin/env bash
#
# verify-arch.sh — assert a compiled binary's ELF architecture matches its
# target platform. Extracted from release.yml so the arch-match decision is
# unit-testable without Docker (see verify-arch_test.sh).
#
# Issue #74: the release pipeline builds multi-arch images, but the Dockerfiles
# once hardcoded GOARCH=amd64, so the arm64 manifest shipped an amd64 binary
# that died with "exec format error" on real aarch64 hosts. This compares
# GNU `file`'s ELF machine description against the token expected for the arch.
#
# Subcommands:
#   token   <amd64|arm64>             -> the `file` substring expected for the arch
#   matches "<file -b output>" <arch> -> exit 0 if the description matches, else 1
#   assert-file <path> <arch>         -> run `file -b <path>` and assert it matches
#
# All decision logic lives in pure functions (arch_token / arch_matches) so the
# test can feed synthetic `file` descriptions without building any image.

set -euo pipefail

# Map a Docker/Go arch to the substring GNU `file` prints for that ELF machine.
# The two tokens are disjoint (neither is a substring of the other's output),
# so a simple containment test is unambiguous.
arch_token() {
  case "$1" in
    amd64) echo "x86-64" ;;
    arm64) echo "aarch64" ;;
    *) echo "verify-arch: unknown arch '$1'" >&2; return 2 ;;
  esac
}

# Does a `file -b` description denote the expected arch? 0 = match, 1 = mismatch.
arch_matches() {
  local desc="$1" arch="$2" token
  token="$(arch_token "$arch")" || return 2
  case "$desc" in
    *"$token"*) return 0 ;;
    *) return 1 ;;
  esac
}

# Run `file` on a binary and assert its arch. Prints the description either way.
assert_file() {
  local path="$1" arch="$2" desc
  desc="$(file -b "$path")"
  echo "  $path: $desc"
  if arch_matches "$desc" "$arch"; then
    echo "  OK: matches $arch"
  else
    echo "::error::$path is not $arch (expected $(arch_token "$arch")): $desc" >&2
    return 1
  fi
}

# CLI dispatch — only when executed directly, not when sourced by the test.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  cmd="${1:-}"
  shift || true
  case "$cmd" in
    token)       arch_token "$@" ;;
    matches)     arch_matches "$@" ;;
    assert-file) assert_file "$@" ;;
    *) echo "usage: verify-arch.sh {token|matches|assert-file} ..." >&2; exit 2 ;;
  esac
fi
