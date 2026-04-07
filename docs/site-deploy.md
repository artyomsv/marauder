# Deploying marauder.cc

The marketing site at [`https://marauder.cc`](https://marauder.cc) is a
static Astro project under [`site/`](../site) that's built and deployed
automatically by [`.github/workflows/site.yml`](../.github/workflows/site.yml).

This document covers the **one-time setup** to point the custom domain
at GitHub Pages, and the **ongoing workflow** for editing the site.

---

## One-time setup

### 1. Enable GitHub Pages with the Actions source

In the repository settings:

1. Go to **Settings → Pages**
2. Source: **GitHub Actions** (not "Deploy from a branch")
3. Save

The first push to `main` that touches `site/**` will run
`.github/workflows/site.yml` and deploy to a temporary
`https://artyomsv.github.io/marauder` URL. This proves the build works
end-to-end before DNS gets involved.

### 2. Add the DNS records at the domain registrar

GitHub Pages uses **anycast IPs for apex domains**, so you set four
A records pointing at the same set of GitHub IPs. The IPs are stable
and published by GitHub at <https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site>.

At your registrar's DNS panel, add:

```
TYPE   NAME              VALUE                       TTL
A      @                 185.199.108.153             3600
A      @                 185.199.109.153             3600
A      @                 185.199.110.153             3600
A      @                 185.199.111.153             3600
AAAA   @                 2606:50c0:8000::153         3600
AAAA   @                 2606:50c0:8001::153         3600
AAAA   @                 2606:50c0:8002::153         3600
AAAA   @                 2606:50c0:8003::153         3600
CNAME  www               artyomsv.github.io.         3600
```

The `@` symbol means "the apex" (i.e., `marauder.cc` itself). The
`CNAME www` record handles the `www.marauder.cc` redirect — GitHub
will 301 it to the apex automatically.

> **Why four A records?** Apex domains can't have CNAME records (RFC
> 1034 prevents an A and CNAME from coexisting on the same name). GitHub
> publishes four anycast IPs and asks you to set them all so traffic is
> distributed across their edge nodes.

### 3. Tell GitHub Pages about the custom domain

The site already includes a [`site/public/CNAME`](../site/public/CNAME)
file containing the line `marauder.cc`. Astro copies it into
`site/dist/CNAME` at build time, GitHub Pages reads it on deploy, and
the custom domain field in **Settings → Pages** is auto-populated.

After DNS propagates (usually 5–60 minutes):

1. Go to **Settings → Pages → Custom domain**
2. You should see `marauder.cc` already filled in
3. Wait for the DNS check to pass (a green checkmark)
4. Tick **Enforce HTTPS**

GitHub will provision a Let's Encrypt certificate within ~10 minutes.
After that, every request to `marauder.cc` is served by GitHub Pages
over HTTPS with HSTS.

### 4. Verify

```bash
# Apex resolves to GitHub Pages
dig +short A marauder.cc
# 185.199.108.153
# 185.199.109.153
# 185.199.110.153
# 185.199.111.153

# www redirects to apex
curl -sI https://www.marauder.cc/ | head -3
# HTTP/2 301
# location: https://marauder.cc/

# HTTPS is enforced and the cert is valid
curl -sI https://marauder.cc/ | head -3
# HTTP/2 200
# content-type: text/html

# robots.txt and sitemap are reachable
curl -fsS https://marauder.cc/robots.txt
curl -fsS https://marauder.cc/sitemap-index.xml
```

### 5. Submit to search engines

Once HTTPS is live:

1. **Google Search Console:** add `https://marauder.cc` as a property
   (verify via DNS TXT or HTML file), then submit
   `https://marauder.cc/sitemap-index.xml`
2. **Bing Webmaster Tools:** same flow
3. **Yandex Webmaster:** same flow (the audience is partly Russian-
   speaking, so Yandex visibility matters)

---

## Ongoing workflow

### Editing a page

```bash
# Local dev (Docker, no Node install needed)
docker run --rm -it -v "$PWD/site:/app" -w /app -p 4321:4321 node:22-alpine \
  sh -c "npm install && npm run dev -- --host 0.0.0.0"
```

Open <http://localhost:4321> and edit `site/src/pages/*.astro`. Astro
hot-reloads on save.

### Adding a new page

1. Create `site/src/pages/<slug>.astro`
2. Import the layout: `import Page from "@/layouts/Page.astro"`
3. Set the SEO frontmatter (`title`, `description`, `path`, `keywords`)
4. Add any page-specific JSON-LD via the `schemas` prop
5. Push to `main`. The site workflow rebuilds and redeploys.

The new page is **automatically added to the sitemap** because
`@astrojs/sitemap` walks `pages/`.

### Updating SEO metadata

The single source of truth is [`site/src/data/seo.ts`](../site/src/data/seo.ts).
Bumping the version number, changing the tagline, or updating the
GitHub URL is a one-line edit there — every page picks it up on the
next build.

### Verifying SEO before pushing

```bash
docker run --rm -v "$PWD/site:/app" -w /app node:22-alpine \
  sh -c "npm ci && npm run build"

# Inspect the rendered <head> of any page
grep -oE '<title>[^<]*</title>' site/dist/index.html
grep -oE '<meta name="description"[^>]*>' site/dist/index.html
grep -oE '<link rel="canonical"[^>]*>' site/dist/index.html
grep -oE '"@type":"[^"]+"' site/dist/index.html
```

### Lighthouse check (optional)

```bash
docker run --rm --network host \
  -v "$PWD/site/dist:/site" \
  -w /site \
  -e CHROME_PATH=/usr/bin/google-chrome \
  patrickhulce/lhci-action:latest \
  lhci autorun --collect.staticDistDir=/site --collect.url=http://localhost
```

Target scores: **Performance ≥ 95**, **Accessibility ≥ 95**,
**Best Practices ≥ 95**, **SEO 100**.

---

## What's where

| File | Purpose |
|---|---|
| `site/astro.config.mjs` | Astro config: site URL, sitemap integration, prefetch, Tailwind |
| `site/src/data/seo.ts` | Sitewide SEO constants (URL, brand name, version, OG image path) |
| `site/src/data/schemas.ts` | JSON-LD builders: Organization, WebSite, SoftwareApplication, BreadcrumbList, FAQPage, HowTo |
| `site/src/components/BaseHead.astro` | Per-page `<head>` tags (title, meta, OG, Twitter Card, canonical) |
| `site/src/components/JsonLd.astro` | XSS-safe `<script type="application/ld+json">` emitter |
| `site/src/layouts/Page.astro` | Base layout that wraps every page |
| `site/src/pages/*.astro` | The 9 routes |
| `site/public/CNAME` | Custom domain marker for GitHub Pages |
| `site/public/robots.txt` | Crawl directives + sitemap pointer |
| `.github/workflows/site.yml` | Build + deploy pipeline |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Workflow runs but Pages says "Source not configured" | First-time setup not done | Settings → Pages → Source: GitHub Actions |
| `marauder.cc` DNS check stuck | DNS not propagated yet | Wait 5–60 min, then re-run the check from the Pages UI |
| Custom domain shows red error | Apex A records missing or pointing elsewhere | Run `dig +short A marauder.cc`, fix at registrar |
| HTTPS toggle disabled | DNS not yet validated, or Let's Encrypt not provisioned | Wait ~10 min after DNS turns green |
| Pages serves the temporary `*.github.io` URL instead of marauder.cc | CNAME file missing from `dist/` | Make sure `site/public/CNAME` is committed and contains exactly `marauder.cc` |
| Build fails on `astro check` | TypeScript error in an .astro file | Run `npm run build` locally; fix the reported file |
| Sitemap missing pages | Page wasn't generated (probably 404.astro) | The sitemap integration excludes `404` by design (see `astro.config.mjs`) |
