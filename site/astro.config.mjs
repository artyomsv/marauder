// @ts-check
import { defineConfig } from "astro/config";
import sitemap from "@astrojs/sitemap";
import tailwindcss from "@tailwindcss/vite";

// Marauder marketing site — astro.config.mjs
//
// SEO is a hard requirement, so:
//   - site URL is set so canonicals + OG URLs + sitemap absolute paths
//     are correct
//   - prefetch is enabled so internal links feel instant
//   - sitemap integration auto-generates sitemap-index.xml at build
//   - no client-side hydration islands by default
//   - Shiki uses our brand-matching theme baked at build time
//
// On every page change, the workflow at .github/workflows/site.yml
// rebuilds and deploys to GitHub Pages, which serves marauder.cc.

export default defineConfig({
  site: "https://marauder.cc",
  trailingSlash: "never",
  prefetch: {
    prefetchAll: true,
    defaultStrategy: "viewport",
  },
  integrations: [
    sitemap({
      changefreq: "weekly",
      priority: 0.7,
      lastmod: new Date(),
      filter: (page) => !page.includes("/404"),
    }),
  ],
  vite: {
    // @ts-expect-error — Astro bundles its own Vite, and the
    // @tailwindcss/vite peer dep version skew confuses tsc here.
    // Runtime works fine; this annotation is purely to satisfy
    // `astro check` until upstream realigns the Plugin type.
    plugins: [tailwindcss()],
  },
  build: {
    inlineStylesheets: "auto",
    format: "directory",
  },
  markdown: {
    shikiConfig: {
      theme: "github-dark-dimmed",
      wrap: true,
    },
  },
});
