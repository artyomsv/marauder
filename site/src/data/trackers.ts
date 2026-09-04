// The tracker plugins bundled with Marauder. Used to render the
// trackers page table and the home-page summary grid.
//
// Entries are removed when the tracker itself stops existing, not merely
// when it has a bad day. Removed 2026-08-03 after probing every domain:
// HD-Club (shut down in 2017; hdclub.org now redirects to an unrelated
// file-hosting affiliate site), Unionpeer (all three domains parked or
// NXDOMAIN) and Free-Torrents (no A record on any resolver).

export type TrackerStatus = "validated" | "alpha";

export type Tracker = {
  slug: string;
  name: string;
  description: string;
  category: "generic" | "forum-cis" | "indexer";
  region: string;
  auth: "public" | "account" | "apikey";
  status: TrackerStatus;
  cloudflare?: boolean;
  quality?: boolean;
};

export const trackers: Tracker[] = [
  // Generic
  {
    slug: "genericmagnet",
    name: "Generic Magnet",
    description: "Accept any magnet URI as a one-shot hand-off.",
    category: "generic",
    region: "Worldwide",
    auth: "public",
    status: "validated",
  },
  {
    slug: "generictorrentfile",
    name: "Generic .torrent URL",
    description: "Watch any HTTPS .torrent file URL by SHA-1 of the file body.",
    category: "generic",
    region: "Worldwide",
    auth: "public",
    status: "validated",
  },
  // CIS forum trackers
  {
    slug: "rutracker",
    name: "RuTracker.org",
    description: "The largest Russian-language forum tracker. Verified end-to-end against a live account.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "validated",
  },
  {
    slug: "kinozal",
    name: "Kinozal.me",
    description:
      "Russian-language tracker for movies and TV. Behind a Cloudflare challenge since September 2026, so it needs a FlareSolverr instance. Verified end-to-end against a live account.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "validated",
    cloudflare: true,
  },
  {
    slug: "nnmclub",
    name: "NNM-Club.to",
    description: "Russian-language phpBB tracker. Works anonymously — no account needed. Verified end-to-end against a live release topic.",
    category: "forum-cis",
    region: "RU",
    auth: "public",
    status: "validated",
    cloudflare: true,
  },
  {
    slug: "lostfilm",
    name: "LostFilm.tv",
    description: "Russian-dubbed TV series with quality selection (SD / 1080p_mp4 / 1080p) and start-from-episode filter. Walks the full v_search redirector chain. Verified end-to-end against a live account, including interactive captcha login.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "validated",
    quality: true,
  },
  {
    slug: "anilibria",
    name: "AniLiberty.top",
    description: "Public anime tracker. Uses the official AniLiberty v1 JSON API and retains legacy Anilibria v3 URL support. Validated read-only against the live v1 API.",
    category: "forum-cis",
    region: "RU",
    auth: "public",
    status: "validated",
  },
  {
    slug: "anidub",
    name: "tr.anidub.com",
    description:
      "Russian-dubbed anime with quality variants. Verified end-to-end against a live account.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "validated",
    quality: true,
  },
  {
    slug: "rutor",
    name: "Rutor",
    description:
      "Public Russian-language tracker — no account needed. Delivers the real .torrent file, plus title and poster.",
    category: "forum-cis",
    region: "RU",
    auth: "public",
    status: "validated",
  },
  {
    slug: "toloka",
    name: "Toloka.to",
    description:
      "Ukrainian tracker (Гуртом). Everything is behind login, so an account is required — Marauder confirms the session and delivers the real .torrent file. Verified end-to-end against a live account.",
    category: "forum-cis",
    region: "UA",
    auth: "account",
    status: "validated",
  },
  {
    slug: "tapochek",
    name: "Tapochek.net",
    description:
      "Russian-language phpBB tracker. Login-gated end to end — a guest sees no torrent data at all — so an account is required. Verified end-to-end against a live account.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "validated",
  },
  // Indexers (Torznab/Newznab)
  {
    slug: "torznab",
    name: "Torznab",
    description: "Any Torznab indexer: Jackett, Prowlarr, NZBHydra2 in torrent mode, direct feeds. 500+ indexers covered.",
    category: "indexer",
    region: "Worldwide",
    auth: "apikey",
    status: "validated",
  },
  {
    slug: "newznab",
    name: "Newznab",
    description: "Any Usenet indexer: NZBGeek, NZBPlanet, DOGnzb, NZBHydra2. NZB drops to a watch folder.",
    category: "indexer",
    region: "Worldwide",
    auth: "apikey",
    status: "validated",
  },
];

export const trackerCount = trackers.length;
export const validatedCount = trackers.filter((t) => t.status === "validated").length;
export const alphaCount = trackers.filter((t) => t.status === "alpha").length;
