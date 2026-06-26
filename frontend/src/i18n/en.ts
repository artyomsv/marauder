const en: Record<string, string> = {
  // App-wide
  "app.name": "marauder",
  "app.tagline": "self-hosted torrent automation",

  // Auth
  "login.welcome": "Welcome back",
  "login.subtitle": "Sign in to your instance to manage your torrent topics.",
  "login.username": "Username",
  "login.password": "Password",
  "login.signIn": "Sign in",
  "login.or": "or",
  "login.signInOIDC": "Sign in with Keycloak",
  "login.legal":
    "By signing in you agree that you are solely responsible for the content you monitor. Marauder does not host any content.",
  "login.signOut": "Sign out",

  // Nav
  "nav.dashboard": "Dashboard",
  "nav.topics": "Topics",
  "nav.clients": "Clients",
  "nav.accounts": "Accounts",
  "nav.notifiers": "Notifiers",
  "nav.system": "System",
  "nav.audit": "Audit log",
  "nav.integrations": "Integrations",
  "nav.settings": "Settings",

  // Dashboard
  "dashboard.section": "overview",
  "dashboard.title": "Good to see you again.",
  "dashboard.subtitle":
    "Here's what Marauder has been watching while you were away.",
  "dashboard.tile.active": "Active topics",
  "dashboard.tile.updates24h": "Updates in 24h",
  "dashboard.tile.errored": "Errored",
  "dashboard.tile.totalTracked": "Total tracked",
  "dashboard.recentActivity": "Recent activity",
  "dashboard.recentActivity.subtitle": "Last 10 topics updated",
  "dashboard.empty.title": "No topics yet",
  "dashboard.empty.body":
    "Head over to the Topics page and paste a tracker URL or magnet link to start watching.",

  // Topics
  "topics.section": "watchlist",
  "topics.title": "Topics",
  "topics.subtitle": "Every URL Marauder is actively monitoring for you.",
  "topics.add": "Add topic",
  "topics.empty.title": "No topics yet",
  "topics.empty.body": "Paste a tracker URL, magnet link, or .torrent URL to start watching.",
  "topics.empty.cta": "Add your first topic",
  "topics.add.title": "Add a new topic",
  "topics.add.url": "URL or magnet link",
  "topics.add.urlPlaceholder":
    "magnet:?xt=urn:btih:... or https://tracker.example.com/.../file.torrent",
  "topics.add.displayName": "Display name (optional)",
  "topics.add.displayNamePlaceholder": "Leave blank to auto-detect",
  "topics.add.cancel": "Cancel",
  "topics.add.submit": "Add topic",
  "topics.col.checked": "checked",
  "topics.col.updated": "updated",

  // Clients
  "clients.section": "delivery",
  "clients.title": "Clients",
  "clients.subtitle":
    "Where Marauder hands torrents off when a topic updates.",
  "clients.add": "Add client",
  "clients.empty.title": "No clients yet",
  "clients.empty.body":
    "Add a torrent client (qBittorrent, Transmission, Deluge, or a download folder) so Marauder has somewhere to send updates.",
  "clients.empty.cta": "Add your first client",
  "clients.testConnection": "Test connection",
  "clients.add.title": "Add a torrent client",
  "clients.add.plugin": "Plugin",
  "clients.add.displayName": "Display name",
  "clients.add.displayNamePlaceholder": "e.g. Living room qBit",
  "clients.add.useDefault": "Use as default client for new topics",
  "clients.add.cancel": "Cancel",
  "clients.add.submit": "Test & save",
  "clients.badge.default": "default",

  // Credentials — interactive captcha login
  "credentials.captchaTitle": "Enter the code from the image",
  "credentials.captchaPlaceholder": "Code from the image",
  "credentials.captchaImageAlt": "Captcha challenge image",
  "credentials.captchaRefresh": "Refresh code",
  "credentials.captchaIncorrect": "Incorrect code, please try again",
  "credentials.captchaSubmit": "Verify & save",
  "credentials.captchaCancel": "Cancel",
  "credentials.sessionExpired": "Session expired",
  "credentials.reauthenticate": "Re-authenticate",

  // Settings
  "integrations.kicker": "connections",
  "integrations.title": "Integrations",
  "integrations.blurb": "Connect Marauder to external services like Sonarr.",
  "settings.kicker": "preferences",
  "settings.title": "Settings",
  "settings.blurb": "Personalise Marauder's appearance, manage your account, and read about this build.",
  "settings.appearance.title": "Appearance",
  "settings.appearance.blurb": "Theme, language, and table density are stored locally in this browser.",
  "settings.appearance.theme": "Theme",
  "settings.appearance.themeLight": "Light",
  "settings.appearance.themeDark": "Dark",
  "settings.appearance.language": "Language",
  "settings.appearance.density": "Table density",
  "settings.appearance.densityComfortable": "Comfortable",
  "settings.appearance.densityCompact": "Compact",
  "settings.account.title": "Account",
  "settings.account.blurb": "Local Marauder account credentials. OIDC users sign in via the identity provider.",
  "settings.account.username": "Username",
  "settings.account.email": "Email",
  "settings.account.changePassword": "Change password",
  "settings.account.currentPassword": "Current password",
  "settings.account.newPassword": "New password",
  "settings.account.confirmPassword": "Confirm new password",
  "settings.account.savePassword": "Update password",
  "settings.account.saving": "Saving...",
  "settings.account.passwordChanged": "Password updated.",
  "settings.account.passwordMismatch": "New password and confirmation do not match.",
  "settings.account.passwordTooShort": "New password must be at least 8 characters.",
  "settings.account.signOut": "Sign out",
  "settings.about.title": "About",
  "settings.about.blurb": "Build metadata and project links.",
  "settings.about.version": "Version",
  "settings.about.license": "License",
  "settings.about.links": "Links",
  "settings.sonarr.title": "Sonarr integration",
  "settings.sonarr.blurb":
    "Let Marauder automatically take over monitoring of updateable forum-tracker topics that Sonarr grabs (RuTracker, Kinozal, …). Admin only.",
  "settings.sonarr.enabled": "Enable integration",
  "settings.sonarr.url": "Sonarr URL",
  "settings.sonarr.apiKey": "API key",
  "settings.sonarr.apiKeySet": "A key is stored — leave blank to keep it.",
  "settings.sonarr.pollInterval": "Poll interval (seconds)",
  "settings.sonarr.allowedTrackers": "Allowed trackers",
  "settings.sonarr.allowedTrackersHint":
    "Leave all unchecked to allow every supported tracker.",
  "settings.sonarr.defaultClient": "Default download client",
  "settings.sonarr.defaultClientNone": "None (use account default)",
  "settings.sonarr.defaultCategory": "Default category",
  "settings.sonarr.defaultDownloadDir": "Default download directory",
  "settings.sonarr.updateExisting": "Realign existing topics to these defaults",
  "settings.sonarr.test": "Test connection",
  "settings.sonarr.testing": "Testing…",
  "settings.sonarr.testOk": "Connected to Sonarr",
  "settings.sonarr.save": "Save",
  "settings.sonarr.saving": "Saving…",
  "settings.sonarr.saved": "Settings saved.",

  // Topic live check status (fed by SSE check.* events)
  "topics.check.checking": "Checking…",
  "topics.check.error": "Check error",
  "topics.check.next": "Next check {time}",

  // Topic delivery status
  "topics.delivery.delivered": "Delivered",
  "topics.delivery.downloading": "Downloading",
  "topics.delivery.finished": "Finished",
  "topics.delivery.count": "{n} delivered",
  "topics.delivery.season": "Season {n}",
  "topics.delivery.reissued": "{n} versions · tracker re-issued this torrent · newest {date}",
  "topics.delivery.deliveredOn": "delivered {date}",
  "topics.delivery.copy": "Copy label",
  "topics.delivery.copied": "Copied",

  // Notifier event subscriptions (which events a notifier fires on)
  "notifiers.events.prefix": "Notifies on",
  "notifiers.notify_on": "Notify on",

  // Canonical event labels (notifier picker + topic timeline)
  "events.topic_added": "topic added",
  "events.check_started": "check started",
  "events.check_completed": "check completed",
  "events.release_found": "new release",
  "events.download_submitted": "sent to client",
  "events.download_progress": "download progress",
  "events.download_completed": "download finished",
  "events.check_failed": "error",
  "events.session_expired": "session expired",

  // Topic history timeline
  "topics.history.empty": "No history yet",
  "topics.history.show": "History",
  "topics.history.hide": "Hide history",

  // Generic
  "common.loading": "Loading...",
  "common.justNow": "just now",
  "common.never": "never",
};

export default en;
