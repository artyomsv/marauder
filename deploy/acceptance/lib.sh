#!/usr/bin/env bash
#
# Shared helpers for the client acceptance harness. Sourced by acceptance.sh;
# the classifier is pure text and is unit-tested by lib_test.sh (no Docker).

# Exit status acceptance.sh reports when the run never reached its assertion
# because the container images could not be fetched. Distinct from 1 ("the
# client really rejected us") so the canary workflow can tell an upstream API
# break from registry trouble and stop filing issues about the wrong thing.
# 75 is EX_TEMPFAIL from sysexits.h — "try again later".
# shellcheck disable=SC2034  # read by acceptance.sh, which sources this file.
ACCEPTANCE_EX_INFRA=75

# acceptance_is_registry_failure <combined-compose-output>
#
# True when the output shows an image manifest or layer could not be fetched,
# rather than a failure of the client under test. Runs pull from two registries
# and either can throttle a GitHub-hosted runner, whose egress IP is shared with
# thousands of other jobs: Docker Hub (postgres, nginx, and the golang/node/
# alpine build bases) and lscr.io (the linuxserver client images). On
# 2026-08-28 it was lscr.io — Docker Hub had already served `db` and `gateway`
# in the same job seconds earlier — and three innocent clients were reported as
# having changed their APIs (issues #166-#168; #119-#121 was the same shape).
#
# Patterns stay narrow on purpose. A broad match here would classify a genuine
# upstream regression as "infrastructure" and silence the canary — the exact
# failure the canary exists to catch.
acceptance_is_registry_failure() {
  local log="$1"
  case "$log" in
    # Registry quota, both the daemon wording and Docker Hub's human one.
    *toomanyrequests*|*"pull rate limit"*) return 0 ;;
    # Registry answered, but unhealthy: 5xx / gateway errors.
    *"received unexpected HTTP status: 5"*|*"Service Unavailable"*|*"502 Bad Gateway"*)
      return 0 ;;
    # Transport to a registry never completed: TLS or DNS.
    *"TLS handshake timeout"*|*"lookup registry-1.docker.io"*|*"lookup auth.docker.io"*\
      |*"lookup lscr.io"*|*"lookup ghcr.io"*) return 0 ;;
    # BuildKit wording for a layer fetch that came back non-200 mid-build,
    # and the classic engine wording for a failed config blob fetch.
    *"httpReadSeeker: failed open"*|*"error pulling image configuration"*) return 0 ;;
  esac
  return 1
}
