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
      "RuTracker is behind a Cloudflare challenge. A FlareSolverr container solves it once and Marauder replays the resulting clearance on its own requests — so it can still log in and still download a real .torrent.",
      "Authenticates with your RuTracker account via the HTML login form and keeps the session cookie (encrypted at rest). If RuTracker asks for a captcha, Marauder shows it to you in-app instead of failing.",
      "Scrapes the topic page on a schedule and hashes the attached .torrent to detect in-place replacements.",
      "When the file changes, hands the torrent straight to qBittorrent, Transmission, Deluge, µTorrent, or a watch folder.",
      "This is Marauder's reference plugin — verified end-to-end against a live RuTracker account.",
    ],
    setup: [
      "Start the solver overlay: `docker compose -f docker-compose.yml -f docker-compose.solver.yml up -d`. It runs FlareSolverr and points Marauder at it in one step — a solver that nothing points at behaves exactly like no solver at all. Already run FlareSolverr elsewhere? Set MARAUDER_FLARESOLVERR_URL instead. Either way it must reach the internet from the same public IP as Marauder.",
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
      {
        question: "Why does RuTracker need FlareSolverr?",
        answer:
          "RuTracker serves a Cloudflare challenge to every plain HTTP client, so Marauder cannot reach the forum on its own. FlareSolverr runs a real browser that solves the challenge once and returns a clearance cookie; Marauder then makes its own requests carrying that cookie. Because the requests are Marauder's own rather than proxied through the browser, they can submit your login and carry the binary .torrent — which is why search and full-quality downloads work rather than falling back to a magnet. The solver must reach the internet from the same address as Marauder, since the clearance is tied to the requesting IP.",
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
      "Watch a Kinozal topic and auto-download new torrents with Marauder. Logs in with your account, scrapes the page, and pushes new releases to qBittorrent, Transmission, Deluge, or µTorrent.",
    keywords: [
      "kinozal monitor",
      "kinozal auto download",
      "kinozal автоскачивание",
      "kinozal tv tracker",
      "kinozal sonarr alternative",
    ],
    intro:
      "Kinozal is a popular Russian-language tracker for movies and TV, reachable at kinozal.me and kinozal.guru since the original kinozal.tv domain stopped resolving. Like other forum trackers, it gates content behind a login and serves it from topic threads rather than an indexer API. Marauder logs in with your Kinozal account and monitors the topic for new .torrent attachments, and can switch between the mirrors from Settings → Tracker domains.",
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
      "Monitor NNM-Club.to topics with no account and, in practice, no solver. Marauder scrapes the topic page anonymously, spots the new torrent, and hands it to your client.",
    keywords: [
      "nnm-club monitor",
      "nnmclub auto download",
      "nnm-club cloudflare",
      "nnmclub автоскачивание",
      "nnm club tracker",
    ],
    intro:
      "NNM-Club.to is a Russian-language phpBB tracker sitting behind Cloudflare. That sounds like it needs a browser in the loop, but it does not: as of August 2026 NNM-Club serves the real topic HTML to an ordinary request, so Marauder watches it with no solver running. No account either — NNM-Club's magnet flow is public, and its login is Turnstile-gated, so Marauder does not support logging in at all. Paste a topic URL and it starts monitoring.",
    howItWorks: [
      "Scrapes the topic page anonymously for magnet-link changes — no login, no account, and no solver in the normal case.",
      "Resolves the real release title and poster from the topic page, so a new topic shows a proper name rather than an id.",
      "Reads the release author's latest reply in the thread and includes it in the update notification, so you see why a torrent was replaced.",
      "Hands new releases to your configured client like any other Marauder topic.",
    ],
    setup: [
      "Nothing to configure — no credentials, and no solver needed for normal use.",
      "Paste the topic URL directly as a new topic.",
      "If your server's address does start getting challenged, run the FlareSolverr solver overlay; Cloudflare policy is per-IP, so the clearance path stays available.",
    ],
    faq: [
      {
        question: "Do I need an NNM-Club account to use this plugin?",
        answer:
          "No, and you cannot add one. NNM-Club's magnet flow is publicly accessible, so Marauder monitors topics without logging in. Its login form is gated by Cloudflare Turnstile, which blocks automated sign-in, so the plugin deliberately does not offer credentials — do not add an NNM-Club account under Accounts.",
      },
      {
        question: "Does NNM-Club need a Cloudflare solver?",
        answer:
          "Not in practice. Verified against the live site on 3 August 2026 with no credentials and no solver: Marauder resolved a real infohash and title from a public release topic, produced a magnet, and fetched the title and poster. Cloudflare policy is dynamic and per-IP, so if your server's address does get challenged you can start the FlareSolverr solver overlay and Marauder will replay the resulting clearance — but that is a fallback, not part of normal setup. RuTracker is the tracker that genuinely requires it.",
      },
    ],
  },
  {
    slug: "rutor",
    description:
      "Monitor Rutor topics with no account and no solver. Marauder searches Rutor, watches a release page for changes, and hands the real .torrent file to your client — not just a magnet.",
    keywords: [
      "rutor monitor",
      "rutor auto download",
      "rutor автоскачивание",
      "rutor поиск",
      "rutor tracker watcher",
      "rutor не работает",
      "rutor зеркало",
    ],
    intro:
      "Rutor is a public Russian-language tracker, and it is the one plugin in Marauder that needs no setup at all — no account, no Cloudflare solver, no API key. Paste a release URL, or search Rutor from inside Marauder, and it watches that page for a new torrent. When one appears, Marauder fetches the real .torrent file from Rutor's download host and hands it straight to qBittorrent, Transmission, Deluge or µTorrent.",
    howItWorks: [
      "Reads the release magnet from the topic page's download block, so the 'similar releases' list further down the page can never be mistaken for the release you are watching.",
      "Upgrades that magnet to the real .torrent file from Rutor's download host, accepting it only when its infohash matches the page magnet — and falling back to the magnet, trackers intact, if no mirror serves a usable file.",
      "Resolves the real release name and poster from the topic page, so a new topic shows a proper title instead of an id.",
      "Searches Rutor by free text from the Add topic screen and hands the chosen result straight into the normal add-topic flow.",
      "Follows Rutor's live mirrors automatically: rutor.info is canonical, new-rutor.org is the fallback, and topics saved against the retired rutor.org are re-pointed for you.",
    ],
    setup: [
      "Nothing to configure — Rutor needs no account, so there is no credential to add.",
      "Paste a Rutor release URL as a new topic, or use the Search trackers tab and pick a result.",
      "Choose the client and category you want, and Marauder does the rest on its normal check schedule.",
    ],
    faq: [
      {
        question: "Do I need a Rutor account?",
        answer:
          "No. Rutor's release pages, .torrent downloads and search are all publicly accessible, so Marauder never asks you for credentials. Verified end to end against the live site on 3 September 2026 with no account: change detection, the real .torrent file, title and poster resolution, and search.",
      },
      {
        question: "Does Marauder give my client a magnet or a real .torrent file?",
        answer:
          "A real .torrent file whenever Rutor serves one, because a magnet has to find peers before a client can even read what it contains. Marauder only accepts the file when its infohash matches the magnet on the release page, so it can never deliver a different torrent than the one it is watching. If no mirror serves a usable file it falls back to the page magnet, announce URLs included.",
      },
      {
        question: "Which Rutor domain does Marauder use?",
        answer:
          "rutor.info by default, with new-rutor.org as a mirror. The old rutor.org is a frozen clone — as of 3 September 2026 it was roughly 17,000 releases behind and answered current topic ids with a 'release does not exist' page — so Marauder no longer sends requests there. Topics you saved against it keep working: they are transparently re-pointed at the live mirror.",
      },
      {
        question: "Can I search Rutor from inside Marauder?",
        answer:
          "Yes. Open Topics, choose Add topic, and switch to the Search trackers tab. Rutor needs no account, so its results appear with no configuration at all — title, size and seeder count — and clicking one prefills the add-topic form.",
      },
    ],
  },
];

export const trackerPageSlugs = trackerPages.map((p) => p.slug);
