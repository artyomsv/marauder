# SEO Improvement Plan — marauder.cc

**Date:** 2026-06-22
**Owner:** Artjoms Stukans
**Source data:** Google Search Console, `sc-domain:marauder.cc`, 90-day window
(2026-03-22 → 2026-06-20), pulled via the `personal` GSC account.

---

## 1. Baseline (where we are)

| Metric | 90 days | 28 days |
|---|---|---|
| Clicks | 17 | 11 |
| Impressions | 587 | 413 |
| Avg position | ~8 | — |
| CTR | ~2.9% | ~2.7% |

Impressions by month: **Apr 106 → May 126 → Jun 355** (≈3× growth in June).
The site is early-stage but Google is surfacing it for more queries every month.

**Technical health is good — do not "fix" it:**
- All 8 key pages return `verdict: PASS / Submitted and indexed`.
- Sitemap (`sitemap-index.xml` → `sitemap-0.xml`) is valid, 15 URLs, correct
  canonicals, `trailingSlash: "always"` consistent.
- Only real housekeeping item: GSC shows the sitemap `isPending` since the
  2026-05-30 submission — re-validate it (P3 below), but it is not blocking
  indexing.

**Audience:** desktop-dominant (527 vs 60 mobile impressions); top countries
UA, RU, BY, GE + FR, NL, BR — a Russian/CIS-leaning, English-and-Russian
self-hoster crowd.

### Top pages (90d)

| Page | Clicks | Impr | CTR | Pos |
|---|---|---|---|---|
| `/install/` | 5 | 50 | 10.0% | 4.8 |
| `/trackers/` | 5 | 56 | 8.9% | 9.0 |
| `/` (home) | 2 | **253** | **0.79%** | 8.0 |
| `/vs/sonarr/` | 2 | 117 | 1.71% | 10.8 |
| `/integrations/` | 1 | 73 | 1.37% | 8.7 |
| `/ru/` | 2 | 42 | 4.76% | 9.0 |
| `/vs/monitorrent/` | 1 | 28 | 3.57% | 5.5 |

### Query clusters (41 queries, ranked by volume + intent fit)

1. **Prowlarr** — `prowlarr vs sonarr` (9), `prowlarr oidc` (3), `prowlarr best trackers`, `indexers for prowlarr`, `prowlarr rutracker`, `rutracker prowlarr` (**1 click**), `prowlarr sonarr`, `prowlarr trackers`, `prowlarr vs sonarr vs radarr`. **No dedicated page.**
2. **qBittorrent SSO/OIDC** — `qbittorrent oidc` (13 impr, pos 7.8), `qbittorrent sso` (4), `prowlarr oidc` (3), `qbittorrent authentication failure radarr`. Product has Keycloak/OIDC; **no dedicated page.**
3. **RuTracker "не работает"** — `rutracker не работает` (3), `rutracker.org не работает`, `rutracker org почему не работает`, `rutracker cloudflare`, `rutor.info не работает`, `rutor торрент`. Russian troubleshooting intent; partly served by `/trackers/rutracker/` but its copy doesn't target "not working".
4. **monitorrent** (23 impr, pos 6.1) — the single best-converting term. Marauder is the successor; `/vs/monitorrent/` sits at pos 5.5.
5. **Sonarr/Radarr comparison** — `sonarr vs radarr`, `radarr vs prowlarr`, `sonarr rutracker`, etc.

---

## 2. Guiding principles

- **Target proven demand, not guesses.** Every workstream below maps to queries
  marauder.cc *already shows for*. We are converting existing impressions, not
  chasing net-new keywords.
- **Truthful copy only** (per `~/.claude/rules/no-synthetic-data.md` and the
  existing `trackerPages.ts` convention: "nothing is invented"). Every feature
  claim must trace to a real backend capability.
- **Complement, not replace.** Keep the established positioning: Marauder fills
  the forum-tracker gap the *arr stack can't reach; it is additive to
  Sonarr/Radarr/Prowlarr.
- **Title length:** page `title` ≤ ~48 chars (BaseHead appends `" | Marauder"`,
  keeping the rendered `<title>` under the 60-char guideline from
  `~/.claude/rules/seo-react.md`).

---

## 3. Workstreams (prioritized)

### P1 — Homepage title + meta description rewrite

**Problem:** The home page is **43% of all impressions** but converts at 0.79%
CTR at position 8. It ranks; it doesn't get clicked. This is the single biggest
lever on the site.

**Evidence:** 253 impr / 2 clicks. Other pages at similar positions (`/ru/` pos
9 → 4.76%, `/trackers/` pos 9 → 8.9%) convert 5–10× better, so the gap is the
SERP snippet, not the ranking.

**Change** — `site/src/pages/index.astro`, the `seo` object (lines 12–16):
- `title`: lead with the converting, brand-adjacent angle. Current:
  `"Self-hosted torrent topic monitor"`. Draft replacement:
  `"Monitorrent alternative · torrent tracker monitor"` (47 chars).
- `description`: rewrite to be benefit/curiosity-driven and front-load the
  differentiator. Draft:
  `"Watch RuTracker, LostFilm, Kinozal & 500+ Torznab/Newznab indexers for new releases and auto-push them to qBittorrent, Transmission or Deluge. Open-source, self-hosted, the forum-tracker monitor the *arr stack can't reach."`
  (keep 120–160 chars — trim to fit).
- Consider adding `"qbittorrent automation"` / `"rutracker auto download"` to
  the homepage `keywords` array (already partly present).

**Target queries:** `monitorrent`, `monitorrent alternative`, broad
`rutracker auto download` / `qbittorrent` terms the home page already surfaces for.

**Effort:** ~30 min. **Success:** home-page CTR ≥ 3% within 6 weeks (≈4× from 0.79%).

---

### P1 — Create `/vs/prowlarr/` and de-diffuse `/vs/sonarr/`

**Problem:** Prowlarr is the **largest query cluster** and has **zero dedicated
page**. `/vs/sonarr/` currently tries to rank for Sonarr + Radarr + Prowlarr in
one title/keyword set — which is why it's stranded at position 10.8 (page 2).

**Evidence:** 10+ distinct Prowlarr queries; `rutracker prowlarr` already
produced a click against a page that doesn't even focus on Prowlarr.

**Change:**
1. **New page** `site/src/pages/vs/prowlarr.astro` (clone the `vs/sonarr.astro`
   structure — `Page` layout, `breadcrumbSchema`, feature matrix, TL;DR,
   coexistence section). Angle, all truthful:
   - Prowlarr is an **indexer manager** — it gives Sonarr/Radarr a unified
     Torznab/Newznab search surface, including Jackett-style shims over some
     forum trackers.
   - Prowlarr/Jackett shims do **search**, not **in-place topic monitoring** —
     they can't watch a RuTracker thread for a swapped `.torrent`. Marauder can.
   - Marauder speaks Torznab + Newznab, so one install reaches both the Prowlarr
     indexer world and the forum-tracker world.
   - Matrix rows that map to real queries: "Forum-tracker topic monitoring",
     "Indexer aggregation (Torznab/Newznab)", "Native OIDC/SSO" (→ `prowlarr oidc`),
     "Cloudflare-gated tracker support".
   - `title`: `"Marauder vs Prowlarr — monitor forum trackers"` (45 chars).
   - `keywords`: `prowlarr forum tracker`, `prowlarr rutracker`,
     `indexers for prowlarr`, `prowlarr best trackers`, `marauder vs prowlarr`,
     `prowlarr oidc`.
2. **Narrow `/vs/sonarr/`** (`site/src/pages/vs/sonarr.astro`) to Sonarr/Radarr
   (the PVRs). Remove Prowlarr from its `title`/`keywords`, add a one-line
   cross-link to the new `/vs/prowlarr/` page. This lets each page rank for a
   focused intent instead of competing with itself.
3. Add `/vs/prowlarr/` to the homepage compare links and the `/vs/` cross-links.
   (Astro `@astrojs/sitemap` will pick it up automatically on build.)

**Target queries:** the entire Prowlarr cluster.

**Effort:** ~2–3 h (new page is mostly content). **Success:** `/vs/prowlarr/`
indexed within 2 weeks; appears for ≥5 Prowlarr queries and `/vs/sonarr/`
average position improves (less self-competition) within 6 weeks.

> ⚠️ Cannibalization watch: after launch, confirm in GSC that `/vs/prowlarr/`
> and `/vs/sonarr/` rank for *different* queries. If they fight over the same
> term, consolidate.

---

### P2 — SSO / OIDC landing content

**Problem:** `qbittorrent oidc` (13 impr, pos 7.8), `qbittorrent sso`,
`prowlarr oidc` are high-intent, low-competition queries, and Marauder genuinely
ships **Keycloak/OIDC SSO**. No page targets this directly — `/integrations/`
only name-drops Keycloak in its meta description.

**Change** (pick one, smaller first):
- **Min:** add a dedicated **OIDC / Keycloak SSO** section to
  `site/src/pages/integrations.astro` with an `id` anchor, explaining native
  OIDC sign-in and how it sits in front of a torrent stack (where qBittorrent /
  Prowlarr need Authelia/reverse-proxy hacks, Marauder has it built in — this is
  already a true matrix row on `/vs/sonarr/`).
- **Fuller:** a standalone `site/src/pages/sso.astro` (or `/oidc/`) targeting
  `self-hosted torrent SSO`, `qbittorrent oidc`, `keycloak torrent`. Higher
  effort, only if the integrations section gains traction first.

**Target queries:** SSO/OIDC cluster. **Effort:** ~1 h (section) / ~2–3 h (page).
**Success:** start ranking for `qbittorrent oidc` / `qbittorrent sso` with the
SSO content as the landing page within 6 weeks.

---

### P2 — RuTracker "не работает" (Russian troubleshooting intent)

**Problem:** A cluster of Russian "not working" queries (`rutracker не работает`,
`rutracker.org не работает`, `rutor.info не работает`, `rutracker cloudflare`)
that the audience geography (RU/UA/BY/GE) confirms is real. `/trackers/rutracker/`
exists but its copy answers "can Sonarr do this", not "RuTracker is down / blocked".

**Change** — extend `trackerPages.ts` (the `rutracker` entry) and/or `/ru/`:
- Add FAQ entries (also emitted as `FAQPage` JSON-LD) answering, truthfully:
  "RuTracker недоступен / заблокирован — как Marauder продолжает качать"
  (session-cookie auth + Cloudflare bypass via the cfsolver sidecar — both real
  capabilities), and the mirror/`.org` situation.
- Strengthen `/ru/` (currently thin, 42 impr) as the Russian-language hub linking
  to the RuTracker page.

**Target queries:** the `не работает` / `cloudflare` cluster. **Effort:** ~1–2 h.
**Success:** `/trackers/rutracker/` or `/ru/` ranks for ≥3 "не работает" queries
within 6 weeks. (Keep all copy truthful — describe only what cfsolver actually does.)

---

### P2 — Push `/vs/monitorrent/` and `/vs/sonarr/` toward the top 10

**Problem:** `monitorrent` (pos 6.1) is the best-converting term and Marauder is
its direct successor; `/vs/monitorrent/` sits at pos 5.5 — close to the top 3.
`/vs/sonarr/` at pos 10.8 is one position-band away from page-1 clicks.

**Change:** internal-linking + depth pass, no new pages:
- Add contextual internal links from the home page and `/trackers/` to
  `/vs/monitorrent/` (anchor text "monitorrent alternative" / "migrating from
  monitorrent").
- Once `/vs/sonarr/` is de-diffused (P1 above), add a short migration/FAQ block
  to deepen it.

**Effort:** ~1 h. **Success:** `/vs/monitorrent/` average position ≤ 4 and
`/vs/sonarr/` ≤ 9 within 6 weeks.

---

### P3 — Housekeeping

- **Sitemap pending:** in GSC → Sitemaps, remove and re-submit
  `sitemap-index.xml` (or hit "validate") to clear the `isPending` state. Pages
  are already indexed; this is cosmetic but tidy.
- **Trailing-slash hygiene:** internal links in `index.astro` use `/install`,
  `/integrations` (no slash) while canonical/sitemap use `/install/`. Harmless
  (server redirects, canonical is correct) but normalize to trailing-slash to
  avoid wasted redirect hops and the minor `/install` vs `/install/` impression
  split seen in GSC.
- **Mobile:** low demand (60 impr) but verify the comparison tables render on
  small screens (they use `overflow-x-auto` already).

---

## 4. Measurement plan

Re-pull GSC at **+4 weeks (2026-07-20)** and **+6 weeks (2026-08-03)** with the
`personal` account, `sc-domain:marauder.cc`. Track:

| Workstream | Primary metric | Target |
|---|---|---|
| Home title/meta | home-page CTR | 0.79% → ≥3% |
| `/vs/prowlarr/` | queries page ranks for; `/vs/sonarr/` avg pos | indexed + ≥5 prowlarr queries; sonarr pos improves |
| SSO/OIDC | ranks for `qbittorrent oidc`/`sso` | landing page appears for the cluster |
| RuTracker RU | ranks for `…не работает` cluster | ≥3 queries |
| monitorrent/sonarr push | avg position | monitorrent ≤4, sonarr ≤9 |

Overall guardrail: total clicks should outpace the impression growth curve (i.e.
CTR trends up, not just impressions).

---

## 5. Out of scope / risks

- **No paid acquisition, no link-buying.** Organic content only.
- **No thin doorway pages** — every new page must carry substantive, truthful,
  differentiated copy (the existing `trackerPages.ts` discipline).
- **Cannibalization risk** on the Prowlarr/Sonarr split — monitored explicitly in
  the P1 workstream.
- **Small-sample caveat:** absolute numbers are tiny, so treat individual query
  positions as directional. The clusters (Prowlarr, OIDC, RuTracker-down) are
  consistent enough across 90 days to act on.

---

## 6. Suggested execution order

1. P1 homepage title/meta (30 min, highest ROI, zero risk).
2. P1 `/vs/prowlarr/` + `/vs/sonarr/` de-diffusion (biggest content gap).
3. P2 OIDC section on `/integrations/`.
4. P2 RuTracker RU FAQ + `/ru/` strengthening.
5. P2 internal-linking pass for monitorrent/sonarr.
6. P3 housekeeping + re-validate sitemap.

Items 1–2 can ship as the first PR; 3–5 as a second; 6 anytime.
```
