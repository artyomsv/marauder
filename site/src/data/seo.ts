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

  /** Software metadata for SoftwareApplication JSON-LD.
   *  `version` is kept in sync with the latest GitHub release automatically by
   *  release.yml's bump-dev-version job (sed + commit) — do not hand-edit it
   *  except to correct drift. */
  software: {
    version: "1.2.0",
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
  /** Optional hreflang alternates for multilingual pages. Each `path` is run
   *  through canonical() so the slash matches the indexed URL. Include an
   *  "x-default" entry pointing at the default-language page. */
  alternates?: { hreflang: string; path: string }[];
  /** Optional <html lang> override (defaults to SITE.htmlLang = "en"). */
  lang?: string;
  /** Optional og:locale override (defaults to SITE.locale = "en_US"). */
  ogLocale?: string;
};

/** Helper to build the canonical URL for a page.
 *
 *  Must emit a trailing slash to match what GitHub Pages serves for
 *  directory-format pages (and the trailingSlash:"always" sitemap). If the
 *  canonical tag and the served URL disagree on the slash, Google ignores the
 *  declared canonical and chooses the redirect target itself. */
export function canonical(path: string): string {
  if (path === "/") return SITE.url + "/";
  const withSlash = path.endsWith("/") ? path : path + "/";
  return SITE.url + withSlash;
}
