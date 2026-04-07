// Single source of truth for site-wide SEO constants. Importing this
// file from BaseHead.astro and JsonLd.astro guarantees the canonical
// host, brand name, and social handles are identical on every page.

export const SITE = {
  /** Production canonical origin. Used for absolute URLs in OG tags,
   *  canonical link, sitemap, and JSON-LD. */
  url: "https://marauder.cc",

  /** Brand name as it should appear in titles. */
  name: "Marauder",

  /** Tagline used as the og:site_name and twitter description fallback. */
  tagline: "Self-hosted torrent topic monitor",

  /** Locale for og:locale and html lang attribute. */
  locale: "en_US",
  htmlLang: "en",

  /** Default OG image (1200×630). */
  defaultOgImage: "/og/default.svg",

  /** GitHub repository — used for outbound links and structured data. */
  github: "https://github.com/artyomsv/marauder",

  /** Author / publisher info for JSON-LD. */
  author: {
    name: "Artjoms Stukans",
    url: "https://github.com/artyomsv",
  },

  /** Software metadata for SoftwareApplication JSON-LD. */
  software: {
    version: "1.0.0",
    license: "MIT",
    operatingSystem: "Linux, Docker, macOS, Windows",
    applicationCategory: "Utility",
    runtime: "Self-hosted",
  },

  /** ISO 8601 release date for structured data + sitemap lastmod. */
  releaseDate: "2026-04-07",
} as const;

export type Page = {
  /** Required: page title without the brand suffix (BaseHead adds it). */
  title: string;
  /** Required: 120-160 char meta description, unique per page. */
  description: string;
  /** Path relative to the site origin, e.g. "/install". */
  path: string;
  /** Optional: per-page OG image path. Falls back to SITE.defaultOgImage. */
  ogImage?: string;
  /** Optional: keywords array — not used by Google but useful for Bing/Yandex. */
  keywords?: string[];
};

/** Helper to build the canonical URL for a page. */
export function canonical(path: string): string {
  if (path === "/") return SITE.url;
  return SITE.url + path.replace(/\/$/, "");
}
