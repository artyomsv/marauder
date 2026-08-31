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
# rather than a failure of the client under test. Every pull in this harness
# goes through the anonymous Docker Hub quota shared by ALL GitHub-hosted
# runners, so `toomanyrequests` reflects the hour of the day, not our client
# plugins (issues #119-#121, then #166-#168 on the same wording).
#
# Patterns stay narrow on purpose. A broad match here would classify a genuine
# upstream regression as "infrastructure" and silence the canary — the exact
# failure the canary exists to catch.
acceptance_is_registry_failure() {
  local log="$1"
  case "$log" in
    # Docker Hub quota, both the daemon wording and the human one.
    *toomanyrequests*|*"pull rate limit"*) return 0 ;;
    # Registry answered, but unhealthy: 5xx / gateway errors.
    *"received unexpected HTTP status: 5"*|*"Service Unavailable"*|*"502 Bad Gateway"*)
      return 0 ;;
    # Transport to the registry never completed: TLS or DNS.
    *"TLS handshake timeout"*|*"lookup registry-1.docker.io"*|*"lookup auth.docker.io"*)
      return 0 ;;
    # BuildKit wording for a layer fetch that came back non-200 mid-build,
    # and the classic engine wording for a failed config blob fetch.
    *"httpReadSeeker: failed open"*|*"error pulling image configuration"*) return 0 ;;
  esac
  return 1
}
