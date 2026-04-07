// Builders for the JSON-LD schemas Marauder cares about.
// Each builder takes structured input and returns a plain object that
// the <JsonLd> component serializes.

import { SITE } from "@/data/seo";

export function organizationSchema() {
  return {
    "@context": "https://schema.org",
    "@type": "Organization",
    name: SITE.name,
    url: SITE.url,
    logo: `${SITE.url}/favicon.svg`,
    sameAs: [SITE.github],
    founder: {
      "@type": "Person",
      name: SITE.author.name,
      url: SITE.author.url,
    },
  };
}

export function websiteSchema() {
  return {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: SITE.name,
    url: SITE.url,
    inLanguage: "en",
    description: SITE.tagline,
    publisher: {
      "@type": "Organization",
      name: SITE.name,
      url: SITE.url,
    },
  };
}

export function softwareApplicationSchema() {
  return {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: SITE.name,
    description:
      "Self-hosted application that monitors torrent forum-tracker topics for updates and automatically delivers torrents to qBittorrent, Transmission, Deluge, uTorrent, or a watch folder.",
    url: SITE.url,
    softwareVersion: SITE.software.version,
    applicationCategory: SITE.software.applicationCategory,
    operatingSystem: SITE.software.operatingSystem,
    license: `https://opensource.org/licenses/${SITE.software.license}`,
    downloadUrl: SITE.github,
    codeRepository: SITE.github,
    programmingLanguage: ["Go", "TypeScript"],
    datePublished: SITE.releaseDate,
    offers: {
      "@type": "Offer",
      price: "0",
      priceCurrency: "USD",
    },
    author: {
      "@type": "Person",
      name: SITE.author.name,
      url: SITE.author.url,
    },
  };
}

export function breadcrumbSchema(items: { name: string; path: string }[]) {
  return {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: items.map((item, index) => ({
      "@type": "ListItem",
      position: index + 1,
      name: item.name,
      item: item.path === "/" ? SITE.url : `${SITE.url}${item.path}`,
    })),
  };
}

export function faqSchema(items: { question: string; answer: string }[]) {
  return {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    mainEntity: items.map((item) => ({
      "@type": "Question",
      name: item.question,
      acceptedAnswer: {
        "@type": "Answer",
        text: item.answer,
      },
    })),
  };
}

export function howToSchema(args: {
  name: string;
  description: string;
  totalTime?: string;
  steps: { name: string; text: string }[];
}) {
  return {
    "@context": "https://schema.org",
    "@type": "HowTo",
    name: args.name,
    description: args.description,
    totalTime: args.totalTime,
    step: args.steps.map((s, i) => ({
      "@type": "HowToStep",
      position: i + 1,
      name: s.name,
      text: s.text,
    })),
  };
}
