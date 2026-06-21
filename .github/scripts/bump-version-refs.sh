#!/usr/bin/env bash
#
# bump-version-refs.sh — stamp a released version into every file that mirrors
# it. Pure in-place file edits, no git; release.yml's bump-dev-version job calls
# this and then commits the results (deploy/* with [skip ci]; site/* without, so
# site.yml redeploys marauder.cc).
#
# Usage: bump-version-refs.sh <X.Y.Z> [repo-root]   (repo-root defaults to ".")
#
# Deliberately NOT touched:
#   - seo.ts `releaseDate` — maps to JSON-LD datePublished (original publish
#     date); must stay stable. Sitemap lastmod uses new Date(), not this.
#   - install.astro illustrative "commit"/"buildDate" example lines.

set -euo pipefail

VER="${1:-}"
ROOT="${2:-.}"

if [[ ! "$VER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 <X.Y.Z> [repo-root]  (got version='$VER')" >&2
  exit 2
fi

SEMVER='[0-9]+\.[0-9]+\.[0-9]+'

# deploy/docker-compose.yml — source-build VERSION arg + marauder-* image tags.
sed -i -E "s|(VERSION: \")[^\"]+(\")|\1${VER}\2|"                 "$ROOT/deploy/docker-compose.yml"
sed -i -E "s#(image: marauder-(backend|frontend|cfsolver):).*#\1${VER}#" "$ROOT/deploy/docker-compose.yml"

# deploy/docker-compose.ghcr.yml — ${MARAUDER_VERSION:-X.Y.Z} fallbacks + the
# "defaults to X.Y.Z below" comment.
sed -i -E "s|(:-)${SEMVER}(})|\1${VER}\2|g"                       "$ROOT/deploy/docker-compose.ghcr.yml"
sed -i -E "s|(defaults to )${SEMVER}|\1${VER}|g"                  "$ROOT/deploy/docker-compose.ghcr.yml"

# deploy/.env.example — the MARAUDER_VERSION pin users copy.
sed -i -E "s|(MARAUDER_VERSION=)${SEMVER}|\1${VER}|"              "$ROOT/deploy/.env.example"

# site/src/data/seo.ts — SITE.software.version (homepage hero + JSON-LD).
sed -i -E "s|(version: \")${SEMVER}(\")|\1${VER}\2|"              "$ROOT/site/src/data/seo.ts"

# site/src/pages/install.astro — example /system/info output + ghcr default note.
sed -i -E "s|(\"version\": \")${SEMVER}(\")|\1${VER}\2|"          "$ROOT/site/src/pages/install.astro"
sed -i -E "s|(defaults to <code>)${SEMVER}(</code>)|\1${VER}\2|"  "$ROOT/site/src/pages/install.astro"

# README.md — only the three CURRENT-version markers. The shields release badge
# (alt text + URL), the bold "**vX.Y.Z**" latest-release marker under Project
# status, and the MARAUDER_VERSION default note. Deliberately anchored so the
# HISTORICAL mentions in the same prose ("v1.0.0 was the initial production
# cut", "v1.0.1 ... first to publish to GHCR") are left untouched.
sed -i -E "s|(Release: v)${SEMVER}|\1${VER}|"                     "$ROOT/README.md"
sed -i -E "s|(badge/release-v)${SEMVER}(-success)|\1${VER}\2|"    "$ROOT/README.md"
sed -i -E "s|(\*\*v)${SEMVER}(\*\*)|\1${VER}\2|"                  "$ROOT/README.md"
sed -i -E "s|(defaults to \`)${SEMVER}(\`)|\1${VER}\2|"           "$ROOT/README.md"

echo "bump-version-refs: stamped ${VER} into deploy/ and site/ version references"
