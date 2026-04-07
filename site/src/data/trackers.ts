// The 16 tracker plugins bundled with Marauder. Used to render the
// trackers page table and the home-page summary grid.

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
    description: "The largest Russian-language forum tracker.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "kinozal",
    name: "Kinozal.tv",
    description: "Russian-language tracker for movies and TV.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "nnmclub",
    name: "NNM-Club.to",
    description: "Russian-language phpBB tracker, Cloudflare-protected.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
    cloudflare: true,
  },
  {
    slug: "lostfilm",
    name: "LostFilm.tv",
    description: "Russian-dubbed TV series with quality selection.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
    quality: true,
  },
  {
    slug: "anilibria",
    name: "Anilibria.tv",
    description: "Public anime tracker. Uses the official v3 JSON API.",
    category: "forum-cis",
    region: "RU",
    auth: "public",
    status: "alpha",
  },
  {
    slug: "anidub",
    name: "tr.anidub.com",
    description: "Russian-dubbed anime with quality variants.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
    quality: true,
  },
  {
    slug: "rutor",
    name: "Rutor.org",
    description: "Public Russian-language tracker, magnet only.",
    category: "forum-cis",
    region: "RU",
    auth: "public",
    status: "alpha",
  },
  {
    slug: "toloka",
    name: "Toloka.to",
    description: "Ukrainian phpBB tracker.",
    category: "forum-cis",
    region: "UA",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "unionpeer",
    name: "Unionpeer.org",
    description: "Russian-language phpBB tracker.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "tapochek",
    name: "Tapochek.net",
    description: "Russian-language tracker for kids' content.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "freetorrents",
    name: "Free-Torrents.org",
    description: "Russian-language phpBB tracker.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
  },
  {
    slug: "hdclub",
    name: "HD-Club.org",
    description: "TBDev/Gazelle-style HD-only private tracker.",
    category: "forum-cis",
    region: "RU",
    auth: "account",
    status: "alpha",
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
