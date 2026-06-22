// Per-tracker landing-page content. Only trackers with substantive,
// differentiated copy get a generated page (see /trackers/[slug].astro) —
// this avoids shipping thin, near-duplicate "doorway" pages. Every fact here
// is derived from the tracker's real capabilities as implemented in the
// backend plugin (auth model, Cloudflare, quality selection, validation
// status); nothing is invented.

export type TrackerPageContent = {
  /** Must match a slug in data/trackers.ts. */
  slug: string;
  /** Unique 120-160 char meta description. */
  description: string;
  /** Per-page keyword hints (Bing/Yandex). */
  keywords: string[];
  /** Lead paragraph — keyword-rich, tracker-specific. */
  intro: string;
  /** How Marauder monitors this specific tracker. */
  howItWorks: string[];
  /** Setup steps tailored to this tracker's auth model. */
  setup: string[];
  /** Tracker-specific Q&A — also emitted as FAQPage JSON-LD. */
  faq: { question: string; answer: string }[];
};

export const trackerPages: TrackerPageContent[] = [
  {
    slug: "rutracker",
    description:
      "Monitor a RuTracker.org topic and auto-download new torrents. Marauder logs in with your account, scrapes the thread, and detects when an uploader replaces the .torrent — then hands it to your client.",
    keywords: [
      "rutracker monitor",
      "rutracker auto download",
      "rutracker автоскачивание",
      "rutracker sonarr",
      "rutracker topic watcher",
      "rutracker не работает",
      "rutracker недоступен",
    ],
    intro:
      "RuTracker.org is the largest Russian-language forum tracker, and its content lives in phpBB topic threads — not in a Torznab API. That's exactly the case the *arr stack can't handle. Marauder logs in with your RuTracker account, watches the specific topic you point it at, and downloads the new .torrent the moment the uploader swaps it in.",
    howItWorks: [
      "Authenticates with your RuTracker account via the HTML login form and keeps the session cookie (encrypted at rest).",
      "Scrapes the topic page on a schedule and hashes the attached .torrent to detect in-place replacements.",
      "When the file changes, hands the torrent straight to qBittorrent, Transmission, Deluge, µTorrent, or a watch folder.",
      "This is Marauder's reference plugin — verified end-to-end against a live RuTracker account.",
    ],
    setup: [
      "Add your RuTracker login under Credentials (stored AES-256-GCM encrypted).",
      "Paste the topic URL (e.g. a series or release thread) as a new topic.",
      "Pick the torrent client and category; Marauder resolves the title and poster automatically.",
    ],
    faq: [
      {
        question: "Can Sonarr monitor a RuTracker topic?",
        answer:
          "No. Sonarr is built around Torznab indexers; a RuTracker forum thread isn't a Torznab feed. Jackett can bolt a search shim onto RuTracker, but it only does search, not in-place topic monitoring. Marauder watches the thread itself and catches the .torrent swap.",
      },
      {
        question: "Does Marauder handle RuTracker season packs that grow over time?",
        answer:
          "Yes. RuTracker often updates a single season-pack .torrent as new episodes are added. Marauder detects the replaced file by hashing the attachment and re-downloads it, so your client always has the latest pack.",
      },
      {
        question: "RuTracker.org недоступен — Marauder поможет?",
        answer:
          "RuTracker чаще всего недоступен из-за блокировок провайдера, а не из-за самого трекера. Marauder обращается к RuTracker с вашего сервера: если сервер может открыть rutracker.org — напрямую, через зеркало или через VPN/прокси на уровне хоста — Marauder входит под вашей учётной записью и следит за темой как обычно. Сам Marauder не обходит блокировки провайдера; он работает там, где у сервера есть доступ к трекеру.",
      },
      {
        question: "What happens when my RuTracker login session expires?",
        answer:
          "Marauder stores the RuTracker session cookie encrypted and re-authenticates with your saved account when the session goes stale, so monitoring keeps running without a manual re-login. If a credential genuinely needs attention, it raises a notification rather than silently failing.",
      },
    ],
  },
  {
    slug: "lostfilm",
    description:
      "Automatically download new LostFilm.tv episodes. Marauder follows a series, picks your quality (SD / 1080p), handles the interactive captcha login, and grabs each new episode as it airs — a modern monitorrent replacement.",
    keywords: [
      "lostfilm auto download",
      "lostfilm автоматическая загрузка",
      "lostfilm monitorrent",
      "lostfilm qbittorrent",
      "lostfilm tracker automation",
    ],
    intro:
      "LostFilm.tv publishes Russian-dubbed TV series on a bespoke per-series site, behind a login and a captcha — not an RSS feed you can hand to Sonarr. Marauder follows a LostFilm series, lets you choose quality and a start-from episode, walks the full v_search redirector chain, and downloads each new episode automatically as it releases.",
    howItWorks: [
      "Solves the interactive captcha login once (in-app), then persists the encrypted session cookie for unattended checks.",
      "Lets you pick quality — SD, 1080p_mp4, or 1080p — and a start-from season/episode so you only get what you want.",
      "Drains every pending episode per check, downloading them in order and tracking which it already delivered.",
      "Verified end-to-end against a live LostFilm account, including the captcha-login flow.",
    ],
    setup: [
      "Add your LostFilm account and solve the one-time captcha under Credentials.",
      "Add the series URL; the season/episode selectors constrain to released episodes.",
      "Choose quality and a torrent client — new episodes flow in automatically from then on.",
    ],
    faq: [
      {
        question: "Is this a replacement for monitorrent's LostFilm plugin?",
        answer:
          "Yes. monitorrent pioneered LostFilm topic monitoring in Python; Marauder reimplements it in Go with quality selection, a start-from-episode filter, interactive captcha login, and live per-episode download tracking. See the monitorrent comparison for the full picture.",
      },
      {
        question: "Does Marauder pick the right quality automatically?",
        answer:
          "You choose the quality profile per topic (SD / 1080p_mp4 / 1080p). Marauder then always pulls that variant for each new episode, so you don't have to select it every week.",
      },
    ],
  },
  {
    slug: "kinozal",
    description:
      "Watch a Kinozal.tv topic and auto-download new torrents with Marauder. Logs in with your account, scrapes the page, and pushes new releases to qBittorrent, Transmission, Deluge, or µTorrent.",
    keywords: [
      "kinozal monitor",
      "kinozal auto download",
      "kinozal автоскачивание",
      "kinozal tv tracker",
      "kinozal sonarr alternative",
    ],
    intro:
      "Kinozal.tv is a popular Russian-language tracker for movies and TV, built on a phpBB-style forum. Like other forum trackers, it gates content behind a login and serves it from topic threads rather than an indexer API. Marauder logs in with your Kinozal account and monitors the topic for new .torrent attachments.",
    howItWorks: [
      "Authenticates with your Kinozal account and reuses the encrypted session for each scheduled check.",
      "Detects in-place .torrent replacements on the topic page and forwards them to your download client.",
      "Runs on the same per-topic schedule and backoff as every other Marauder tracker.",
    ],
    setup: [
      "Add your Kinozal credentials under Credentials (encrypted at rest).",
      "Paste the topic URL as a new topic and pick your client.",
      "Marauder resolves the display title and starts watching on the next tick.",
    ],
    faq: [
      {
        question: "Is the Kinozal integration ready to use?",
        answer:
          "Yes. The Kinozal plugin is verified end-to-end against a live account — login, infohash detection (read from Kinozal's get_srv_details endpoint), metadata (title + poster), and download to your torrent client are all confirmed working as of June 2026.",
      },
    ],
  },
  {
    slug: "nnmclub",
    description:
      "Monitor NNM-Club.to topics behind Cloudflare. Marauder routes through a headless-Chromium solver to pass the interstitial, logs in, and auto-downloads new torrents to your client.",
    keywords: [
      "nnm-club monitor",
      "nnmclub auto download",
      "nnm-club cloudflare",
      "nnmclub автоскачивание",
      "nnm club tracker",
    ],
    intro:
      "NNM-Club.to is a Russian-language phpBB tracker sitting behind a Cloudflare interstitial — which makes it doubly hard for conventional tools to reach. Marauder pairs a dedicated Cloudflare solver sidecar with its forum plugin to pass the challenge, log in, and watch the topic for new releases.",
    howItWorks: [
      "Routes requests through the cfsolver sidecar (headless Chromium via chromedp) to clear the Cloudflare interstitial and collect cookies.",
      "Logs in with your NNM-Club account and scrapes the topic page for .torrent changes.",
      "Hands new releases to your configured client like any other Marauder topic.",
    ],
    setup: [
      "Enable the cfsolver compose profile (Cloudflare solver sidecar).",
      "Add your NNM-Club credentials under Credentials.",
      "Paste the topic URL; Marauder handles the Cloudflare challenge transparently on each check.",
    ],
    faq: [
      {
        question: "How does Marauder get past Cloudflare on NNM-Club?",
        answer:
          "A separate cfsolver service runs headless Chromium, drives the target URL through the Cloudflare interstitial, and returns the resulting cookies. The NNM-Club plugin opts into this via the WithCloudflare capability — no global browser dependency in the main binary.",
      },
    ],
  },
];

export const trackerPageSlugs = trackerPages.map((p) => p.slug);
