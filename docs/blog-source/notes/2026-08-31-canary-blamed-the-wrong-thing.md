---
date: 2026-08-31
project: marauder
category: [war-story, architecture]
status: note
sources:
  - "deploy/acceptance/acceptance.sh"
  - "deploy/acceptance/lib.sh"
  - ".github/workflows/client-acceptance.yml"
  - "commit:ba86f42"
  - "issue:#119"
  - "issue:#166"
related:
  - "silent-no-op-guards"
---

# My canary filed three bug reports about software it never contacted

**Why interesting:** A nightly job accused qBittorrent, Transmission and Deluge
of breaking their APIs. All three were innocent. The runner had been refused the
container images by a registry rate limit, so not one of those programs was ever
started. The test harness had a single exit code for "the thing under test is
broken" and "I could not obtain the thing under test", and a monitor that cannot
tell those apart does not report faults — it manufactures them.

**Substance:** The same failure had already happened once and been "fixed". That
fix added an authenticated registry login guarded by `if: env.TOKEN != ''`, and
the token was never created — so the protective step was silently skipped every
night for six weeks while looking present in the config. The second half of the
fix was an inference: "the pinned baseline passed, so a later failure must be
real." Rate limits are time-based, so the quota ran out *between* the two stages
and the inference held the wrong way round. The durable fix was neither
credentials nor a smarter heuristic: it was giving the harness a second failure
exit code, and teaching the classifier narrow, evidence-backed patterns — narrow
because a generous "looks like infrastructure" match would silence the canary for
real regressions, converting a noisy monitor into a useless one.

## Sources
- `deploy/acceptance/lib.sh` — the registry-failure classifier and `EX_TEMPFAIL`
- `deploy/acceptance/lib_test.sh` — tested against the real log lines that mis-filed
- `.github/workflows/client-acceptance.yml` — three outcomes: pass, fail, infra
- commit `ba86f42` — the earlier fix whose guard silently no-opped
