# Tracker domains & mirror fallback

Trackers move. A primary domain gets blocked in your region, goes down for a
few days, or the operators rotate to a new one — and suddenly every topic you
monitor on that tracker stops updating. Marauder lets you point a tracker at a
working **mirror** without recreating a single topic, and rotates to the next
mirror automatically when the current one starts failing.

> **Admin only.** Domain configuration is instance-wide — it applies to every
> user's topics on that tracker. It lives under **Settings → Tracker domains**.

## What it does

- **Pick the active domain per tracker** — choose the plugin default or any
  known mirror from a dropdown (e.g. Kinozal ships `kinozal.tv`, `kinozal.me`,
  `kinozal.guru`; RuTracker ships `.org`, `.net`, `.nl`, `.cr`).
- **Add your own mirrors** — enter a custom hostname if the one you need isn't
  in the built-in list.
- **Existing topics follow automatically** — Marauder identifies topics by
  tracker + topic id, not by hostname, so switching the domain reroutes every
  existing topic's checks and downloads immediately. Nothing to recreate.
- **Automatic fallback** — when checks against the active domain fail with
  network errors (timeouts, connection refused, unreachable), Marauder rotates
  to the next mirror in the ring. It waits for a couple of failures before
  switching (so a single blip doesn't move everything) and won't rotate again
  for a 10-minute cooldown. When it rotates, the admin gets a notification.
- **Test before you commit** — the **Test** button probes a domain and reports
  whether it actually serves a real page, not just whether it answers. A dead
  mirror that returns an empty `200` is reported as *"empty page"*, not a false
  "reachable".

## How to use it

1. Open **Settings** as an admin and find the **Tracker domains** section.
2. Click the tracker you want to change — the row expands.
3. Pick a domain from **Active domain**, or type a mirror under **Mirrors** and
   press **Add**.
4. Optionally press **Test** to confirm the domain serves a working page.

That's it — the change is saved instantly and every topic on that tracker uses
the new domain on its next check.

## Good to know

- **"Test" verifies a real homepage, not every feature.** A mirror can serve a
  valid front page while its download endpoints are broken. Test catches the
  common failure modes (dead/empty mirror, `4xx`/`5xx`, redirect stubs); it
  can't guarantee logins and downloads work on that mirror.
- **Cookie-session logins may need re-auth after a switch.** Trackers that use
  an interactive (captcha) login store a domain-scoped session cookie; switching
  domains can invalidate it, and Marauder will prompt you to re-authenticate via
  the usual expiry flow. Password logins re-establish automatically.
- **Custom domains are admin-entered and validated** (hostname only — no scheme,
  port, or path). They extend the tracker's allowlist; Marauder never follows an
  arbitrary URL a topic happens to carry.
