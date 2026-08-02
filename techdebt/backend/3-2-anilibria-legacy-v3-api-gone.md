# Legacy Anilibria v3 API is gone (410), breaking pre-AniLiberty topics

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Small |
| Location | `backend/internal/plugins/trackers/anilibria/anilibria.go` (`apiBase`, `effectiveAPIBase`, the non-AniLiberty branch of `Check`/`Download`) |
| Found during | Tracker availability audit, 2026-08-03 |
| Date | 2026-08-03 |

## Issue

The `anilibria` plugin serves two topic shapes:

- **AniLiberty** (`aniliberty.top/release/<alias>`) — routed by
  `aniLibertyAlias` through the v1 API. **Healthy.**
- **Legacy Anilibria** (`anilibria.tv/release/<slug>.html`) — routed through
  `p.effectiveAPIBase()`, which is the compiled
  `apiBase = "https://api.anilibria.tv/v3"`. **Dead.**

Measured 2026-08-03:

```
GET https://api.anilibria.tv/v3/title?code=one-piece
410 Gone
{"error":{"code":410,"message":"API version is deprecated. Use anilibria.top/api/docs/v1"}}
```

For comparison, the v1 endpoint the healthy branch uses answers normally:

```
GET https://aniliberty.top/api/v1/app/search/releases?query=one   ->  200, 53 KB
```

So any topic still stored with an `anilibria.tv/release/*.html` URL fails
every check. `Check` calls `p.fetch`, which returns a non-2xx error, and the
topic parks on the scheduler's exponential backoff (capped at 6h) reporting a
generic failure.

Note `effectiveAPIBase` makes this worse in one specific case: when an admin
sets an active domain for the tracker it builds `https://api.<domain>/v3` —
i.e. it constructs a *new* v3 URL against the configured host. There is no
configuration of the current code that reaches v1 for a legacy topic.

## Risks

Low blast radius, medium annoyance. It only affects topics created before the
AniLiberty rename; new topics added from `aniliberty.top` are unaffected, and
the plugin is public/read-only so there is no credential or security angle.

The failure is silent in the sense that matters: the topic looks configured
and enabled, and the UI shows a check error rather than "this URL shape is no
longer supported", so the user has no signal that the fix is to re-add the
topic from its AniLiberty URL.

## Suggested Solutions

1. **Map legacy slugs onto v1** (preferred) — the v1 API addresses releases by
   alias, and the legacy `<slug>` in `anilibria.tv/release/<slug>.html` is the
   same alias string. Route the legacy branch through the same v1 client the
   AniLiberty branch uses and delete `apiBase`/`effectiveAPIBase` entirely.
   Needs live verification that legacy slugs and v1 aliases really do coincide
   for a sample of real releases — do not assume it.
2. **Detect and report** — keep the current routing but special-case the 410
   into a typed error the frontend renders as "this Anilibria URL is no longer
   supported, re-add the topic from aniliberty.top". Cheap, honest, leaves the
   user with manual work.
3. **Migrate stored URLs** — a one-shot migration rewriting
   `anilibria.tv/release/<slug>.html` to the AniLiberty equivalent. Only safe
   on top of option 1's verification, since it bakes in the alias assumption.

Option 1 is preferred, gated on the alias check. Not bundled with the
2026-08-03 tracker-removal work because that change is "delete trackers that
no longer exist", while this is "migrate a live tracker's legacy URL shape" —
different risk, and this one needs live-API verification before it can be
written.
