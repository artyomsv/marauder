/**
 * Centralized React Query keys.
 *
 * Always import from here instead of using inline string literals so a
 * typo in `useQuery({ queryKey: [...] })` or `invalidateQueries({...})`
 * can't silently break refetching.
 *
 * Keys are returned as `as const` tuples so TypeScript catches mismatches
 * between the produce-site (`useQuery`) and the consume-site
 * (`invalidateQueries`) at compile time.
 */

export const QK = {
  // Topics watchlist (list endpoint).
  topics: ["topics"] as const,

  // Torrent clients (list + per-id detail).
  clients: ["clients"] as const,
  client: (id: string) => ["client", id] as const,

  // Tracker credentials.
  credentials: ["credentials"] as const,

  // Notifier configurations.
  notifiers: ["notifiers"] as const,
  notifier: (id: string) => ["notifier", id] as const,

  // Public /system/info — version, plugin manifests. Shared by AppShell
  // version chip and Settings About card.
  systemInfo: ["system-info"] as const,

  // Authenticated /system/status — runtime metrics, scheduler health.
  systemStatus: ["system-status"] as const,

  // Audit log entries (admin only).
  audit: ["audit"] as const,

  // /trackers/match?url=… lookup. Used by Topics AddTopicCard to
  // detect which plugin handles a pasted URL.
  trackerMatch: (url: string) => ["tracker-match", url] as const,

  // /trackers/seasons?url=… released-season catalog. Used by AddTopicCard
  // to populate the dependent season → episode dropdowns.
  trackerSeasons: (url: string) => ["trackerSeasons", url] as const,

  // /trackers/preview?url=… resolved title + poster image. Used by the
  // AddTopic form to show a preview card before the topic is created.
  trackerPreview: (url: string) => ["trackerPreview", url] as const,

  // /topics/{id}/status — delivered torrents + live download progress.
  // Polled while a topic has an in-progress download and the row is shown.
  topicStatus: (id: string) => ["topicStatus", id] as const,
} as const;
