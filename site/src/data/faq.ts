export type FaqItem = {
  question: string;
  answer: string;
};

export const homeFaq: FaqItem[] = [
  {
    question: "What is Marauder?",
    answer:
      "Marauder is a self-hosted application that monitors torrent forum-tracker topics for updates and automatically delivers the resulting torrent or magnet link to your torrent client. It is built with Go on the backend and React on the frontend, and runs as a four-container Docker compose stack.",
  },
  {
    question: "How is Marauder different from Sonarr or Radarr?",
    answer:
      "Sonarr and Radarr are built around Torznab indexers. They cannot monitor a forum thread on RuTracker, LostFilm, or NNM-Club because those sites are forums, not API-driven indexers. Marauder is built specifically to watch forum threads, scrape topic pages, and detect when an uploader replaces the .torrent attachment — logging in with your account where the tracker requires it, or fetching anonymously where it does not (NNM-Club). It also speaks Torznab and Newznab so you can use it on top of Jackett or Prowlarr if you want.",
  },
  {
    question: "Is Marauder a replacement for monitorrent?",
    answer:
      "Yes. Marauder is a modern, independently built successor to monitorrent — the Python torrent-topic monitor many self-hosters still run but which is no longer actively maintained. It keeps monitorrent's core idea (watch a forum-tracker topic such as a LostFilm or RuTracker thread, and grab the new .torrent the moment an uploader replaces it) and rebuilds it in Go with a React UI, AES-256-GCM encrypted credential storage, OIDC sign-in, Prometheus metrics, and live per-episode download tracking. If you run monitorrent today, Marauder covers the same job on a modern, actively maintained, Docker-native stack.",
  },
  {
    question: "Which trackers does Marauder support?",
    answer:
      "13 trackers: RuTracker, Kinozal, NNM-Club, LostFilm, AniLiberty, Anidub, Rutor, Toloka, Tapochek, plus generic .torrent and magnet URLs, plus Torznab and Newznab indexers (which together cover 500+ sites via Jackett, Prowlarr, NZBHydra2).",
  },
  {
    question: "Which torrent clients does Marauder support?",
    answer:
      "qBittorrent (WebUI API v2), Transmission (RPC), Deluge (Web JSON-RPC), µTorrent (token-based WebUI), and a download-to-folder fallback that pairs with SABnzbd or NZBGet for Usenet.",
  },
  {
    question: "Can Marauder notify me when a topic updates?",
    answer:
      "Yes. Marauder sends notifications through Telegram, email (SMTP), webhooks, or Pushover — on a new release or a topic error — and you choose which events each notifier listens for. Mark a default notifier per type (one Telegram, one email, and so on) and topics notify those defaults; override a single topic to route its alerts to one specific notifier instead, so you can send one show to a separate chat while the rest stay on your defaults. Notifiers are fully editable — change channels, events, or the default flag any time.",
  },
  {
    question: "Is Marauder free?",
    answer:
      "Yes. Marauder is open source under the Apache License 2.0. There is no paid tier, no hosted version, no telemetry. You self-host it on your own machine.",
  },
  {
    question: "How do I install it?",
    answer:
      "Clone the GitHub repository, copy deploy/.env.example to .env, generate a master key with `openssl rand -base64 32`, and run `docker compose up -d`. The full quick-start is at marauder.cc/install.",
  },
  {
    question: "Does Marauder support OIDC / Keycloak login?",
    answer:
      "Yes. Marauder ships with first-class OpenID Connect support via coreos/go-oidc. The dev compose stack includes a pre-built Keycloak realm with a test user so you can verify the flow end-to-end in five minutes.",
  },
  {
    question: "Does Marauder handle Cloudflare-protected trackers?",
    answer:
      "Yes. A FlareSolverr container solves the challenge once and returns a clearance cookie plus the browser User-Agent it was issued for; Marauder then replays that pair on its own requests rather than proxying through the browser, which is what lets it submit a login and carry a binary .torrent instead of degrading to a magnet. Start it with the solver overlay, which runs FlareSolverr and points Marauder at it in one step. RuTracker needs this. NNM-Club sits behind Cloudflare too but is not challenged in practice, so it needs no solver at all — and because Cloudflare policy is per-IP, the clearance path stays available if your server does get challenged.",
  },
];
