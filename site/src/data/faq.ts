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
      "Sonarr and Radarr are built around Torznab indexers. They cannot monitor a forum thread on RuTracker, LostFilm, or NNM-Club because those sites are forums, not API-driven indexers. Marauder is built specifically to watch forum threads, log in with your credentials, scrape topic pages, and detect when an uploader replaces the .torrent attachment. It also speaks Torznab and Newznab so you can use it on top of Jackett or Prowlarr if you want.",
  },
  {
    question: "Which trackers does Marauder support?",
    answer:
      "16 trackers in v1.0: RuTracker, Kinozal, NNM-Club, LostFilm, Anilibria, Anidub, Rutor, Toloka, Unionpeer, Tapochek, Free-Torrents, HD-Club, plus generic .torrent and magnet URLs, plus Torznab and Newznab indexers (which together cover 500+ sites via Jackett, Prowlarr, NZBHydra2).",
  },
  {
    question: "Which torrent clients does Marauder support?",
    answer:
      "qBittorrent (WebUI API v2), Transmission (RPC), Deluge (Web JSON-RPC), µTorrent (token-based WebUI), and a download-to-folder fallback that pairs with SABnzbd or NZBGet for Usenet.",
  },
  {
    question: "Is Marauder free?",
    answer:
      "Yes. Marauder is open source under the MIT License. There is no paid tier, no hosted version, no telemetry. You self-host it on your own machine.",
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
      "Yes. A separate cfsolver sidecar service runs headless Chromium via chromedp, drives the target URL through any Cloudflare interstitial, and returns the resulting cookies. Tracker plugins that opt into the WithCloudflare capability automatically route through the solver.",
  },
];
