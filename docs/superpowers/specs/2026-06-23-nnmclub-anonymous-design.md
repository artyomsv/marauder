# NNM-Club tracker — Phase 1 (anonymous) design

- **Date:** 2026-06-23
- **Status:** Phase 1 implemented (anonymous). **Phase 2 (authenticated) ABANDONED — see below.**
- **Scope:** Make the existing `nnmclub` tracker plugin work for everything an
  unauthenticated user can reach. Authenticated `.torrent` / passkey'd-announce flow was
  explored as Phase 2 and dropped.

## Phase 2 (authenticated) — ABANDONED (2026-06-23)

NNM-Club's `login.php` is gated by a **Cloudflare Turnstile** widget that fingerprints the
browser (TLS/HTTP-2 + automation protocol) *before* JS runs. Every automated path was tested
and failed or is structurally impossible:

- **cfsolver headless login (chromedp):** Turnstile never solved.
- **patchright (undetected Chromium) + camoufox (anti-detect Firefox), headless AND real
  virtual display, from the user's own residential IP:** Turnstile widget never even rendered
  (`iframes:0`, `401` from the challenge platform). Cloudflare detects the automation protocol
  regardless of IP/stealth.
- **In-app interactive (relay the token / iframe):** structurally impossible — Turnstile tokens
  are origin-bound to nnmclub.to + Same-Origin-Policy/`X-Frame-Options: SAMEORIGIN` block reading
  the token or cookie from a cross-origin frame.
- **Paid solver service (CapSolver/2Captcha):** would work, but rejected — costs money + 3rd-party
  dependency in a free, self-hosted product.

**Decision:** keep NNM-Club **anonymous-only**. The plugin does NOT implement
`registry.WithCredentials`; the add-account UI shows a disclaimer. The only viable free path
left (not built) is a **companion browser extension / Tampermonkey userscript** that reads the
session cookie from the user's real (Turnstile-passed) browser via `chrome.cookies`/`GM_cookie`
and syncs it to Marauder — revisit if/when there's demand. Methodology note saved:
`memory/browser-probe-cache-nostore.md`.

Original Phase-1 design follows.

## Problem

`backend/internal/plugins/trackers/nnmclub/` already exists, is registered in
`cmd/server/main.go`, and ships green unit tests — but it was never validated against the
live site (its own godoc, `nnmclub.go:8-11`, says so). The tests pass against a **fabricated
fixture** that does not match the real page, so they prove nothing. Confirmed live:
registering a topic and checking it returns `nnm-club: no infohash found`.

### Verified live-site facts (`https://nnmclub.to/forum/viewtopic.php?t=420880`, unauthenticated)

| Fact | Value |
|---|---|
| Magnet present anonymously | ✅ `magnet:?xt=urn:btih:094EC3052ED759240E4DFD89F3F7CA5C5B428FF4` (hash-only) |
| Magnet shape (anonymous) | **hash-only** — no `&tr=`, no `&dn=`. Trackers appear only for logged-in sessions. |
| Infohash as a text label (`Info-Hash:`) | ❌ absent — the 40-hex appears **only** inside the magnet `href` |
| `.torrent` link | `download.php?id=379398` present, but **302-redirects to login** for anon users |
| `og:image` (poster) | ✅ present, content host `a.radikal.ru` |
| Title | `<title>…Оригинальная версия :: NNM-Club</title>` (strip ` :: NNM-Club`) |
| Cloudflare | `server: cloudflare`, `cf-ray` present (real browser passed without a challenge) |

### Anonymous magnet is hash-only; trackers require login (verified twice)

A **fresh** anonymous fetch of the topic page yields a **hash-only** magnet. Confirmed three
ways, all ~74 KB with `tr` count 0: `curl` with our `Marauder/0.3` UA, `curl` with a Chrome
UA, and a browser `fetch(…, {cache:'no-store', credentials:'omit'})`.

> **Lesson — cache artifact:** an earlier investigation used `fetch(…, {credentials:'omit'})`
> *without* `cache:'no-store'` and saw a magnet *with* `bt.searchtor.to` trackers. That was a
> **cached copy of a logged-in response**, not the anonymous page. Always pass `cache:'no-store'`
> when probing what an anonymous/server-side client receives. The logged-in magnet's
> `/<32-hex>/announce` key is a candidate **per-user passkey** and must never be committed.

Consequence: the anonymous magnet is hash-only, so Phase 1 delivery relies on **DHT** — fine
for well-seeded torrents, at risk of stalling on poorly-seeded ones. The reliable fix (and
ratio credit) is the authenticated `.torrent` with the user's passkey'd announce — **Phase 2**.

## Goals / Non-goals

**Goals (Phase 1):**
- `Check` returns the correct infohash + clean title from the anonymous page.
- `Download` returns the anonymous (hash-only) magnet, enriched with a real `&dn=<title>`.
- `WithMetadata.ResolveMetadata` returns real title + poster for the preview card.
- Tests run against a **real captured page fixture**, not invented HTML.
- One live smoke check against `nnmclub.to` confirms a real infohash end-to-end.

**Non-goals (deferred to Phase 2):**
- Login / `Verify` validation against the live site.
- Credentialed `.torrent` download (with `infohash.FromTorrent` validation + magnet fallback).
- Passkey'd `.torrent` announce — the reliability fix for the DHT-stall risk **and** ratio credit.

## Design (Approach A — surgical fixes to the existing plugin)

The plugin already mirrors RuTracker's structure (forumcommon sessions, regex parsing,
registration). Fix the three real defects in place and add `WithMetadata`.

### 1. `Check` — fix hash extraction (`nnmclub.go:141,155`)
- Replace `hashRe` (the non-existent `Info-Hash` text label) with magnet extraction:
  `magnet:\?xt=urn:btih:([A-Fa-f0-9]{40})`, tolerant of `href=['"]`.
- Route the `<title>` through `forumcommon.CleanTitle(raw, " :: NNM-Club")` so cp1251 bytes
  from the Go client decode correctly (current bare `TrimSpace` risks mojibake server-side).
- Error `nnm-club: no infohash found` only when the magnet is genuinely absent.

### 2. `Download` — return enriched magnet (`nnmclub.go:163`)
- Phase 1 (no creds): extract the same infohash, build
  `magnet:?xt=urn:btih:<hash>&dn=<url-encoded real title>`, return
  `domain.Payload{MagnetURI: …}`. **Do not** call `download.php` anonymously (it 302s to login,
  yielding login HTML, not a torrent).
- Structure the method so Phase 2 can slot a credentialed `.torrent`-preferred branch on top
  (mirroring RuTracker's validated path + magnet fallback). Phase 1 ships only the magnet branch.

### 3. `WithMetadata.ResolveMetadata` — new (mirrors `rutracker.go:220`)
- Anonymous fetch → real title (`CleanTitle`) + poster from `og:image` (primary selector;
  confirmed present, host `a.radikal.ru`).
- Powers `/api/v1/trackers/preview`; `/trackers/match` already auto-detects the capability via
  type assertion. Add `nnmclub` to the `WithMetadata` list in `CLAUDE.md`.

### 4. Tests — honest fixtures (`nnmclub_test.go`)
- **Replace** the fabricated `<div>Info-Hash: …</div>` fixture with HTML **captured from the
  real `t=420880` page** (trimmed; public movie data). This is the core anti-regression: the
  regex is forced to work against ground truth, including the single-quoted magnet `href`.
- Unit tests: `Check` extracts the real btih + clean title; `Download` returns a magnet carrying
  that hash + `&dn=`; `ResolveMetadata` returns title + poster.
- e2e: `RunFullPipeline` with a magnet-payload expectation. Verify the `e2etest` harness supports
  a magnet payload; if it only asserts `ExpectTorrentFile`, add a magnet assertion path.

### 5. Cloudflare + live smoke (Definition of Done)
- Keep `UsesCloudflare() == true`. Final gate: one live `Check`+`Download` against `nnmclub.to`
  through the running backend. If a plain Go GET is CF-challenged, that is the trigger to validate
  the cfsolver route. Phase 1 is done when unit + e2e are green on real fixtures **and** the live
  smoke returns a real infohash.

## Error handling

- `Check` fail-closed on missing magnet → scheduler backs off per existing design.
- All outbound calls keep the existing per-request context + `forumcommon` 30s client timeout.
- Anonymous magnet-only delivery's DHT-stall risk is documented honestly in godoc; no fabricated trackers.

## Risks

- **DHT-stall (anonymous hash-only magnet):** well-seeded torrents download via DHT; poorly-seeded
  ones may stall. Resolved by Phase 2's authenticated `.torrent` (passkey'd announce), which also
  credits the user's NNM-Club ratio.
- **Cloudflare challenge on server-side GET:** unknown until live smoke; mitigated by the existing
  `WithCloudflare` + cfsolver route.
- **cp1251 vs UTF-8 title:** `CleanTitle` is idempotent on already-valid UTF-8, so safe either way.

## Files touched (Phase 1)

- `backend/internal/plugins/trackers/nnmclub/nnmclub.go` — Check, Download, ResolveMetadata, regexes.
- `backend/internal/plugins/trackers/nnmclub/nnmclub_test.go` — real fixture + rewritten assertions.
- `backend/internal/plugins/trackers/nnmclub/nnmclub_e2e_test.go` — magnet-payload pipeline assertion.
- `CLAUDE.md` — add `nnmclub` to the `WithMetadata` tracker list.
