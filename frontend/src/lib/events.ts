// Canonical notifier-subscribable checkbox keys (the four UI groups).
export const NOTIFIABLE_EVENTS = [
  "release.found",
  "download.submitted",
  "download.completed",
  "check.failed",
] as const;

export type NotifiableEvent = (typeof NOTIFIABLE_EVENTS)[number];

// Each checkbox key maps to the canonical backend event(s) it stores. The
// "check.failed" box covers BOTH error events so session.expired alerts are
// delivered when the user opts into "errors" — without this, the backend's
// distinct session.expired event would silently never match.
export const EVENT_GROUP_EVENTS: Record<NotifiableEvent, string[]> = {
  "release.found": ["release.found"],
  "download.submitted": ["download.submitted"],
  "download.completed": ["download.completed"],
  "check.failed": ["check.failed", "session.expired"],
};

// Flattened canonical default: every notifiable backend event. Use as the
// initial subscription for a new notifier so all groups (incl. session.expired)
// are on by default — matching the backend's empty-input default.
export const ALL_NOTIFIABLE_EVENTS: string[] = Object.values(EVENT_GROUP_EVENTS).flat();

// i18n keys for each event label, used by the notifier picker and the
// per-topic timeline. Timeline-only (non-notifiable) events are included so
// the history view can render them too.
export const EVENT_LABELS: Record<string, string> = {
  "topic.added": "events.topic_added",
  "check.started": "events.check_started",
  "check.completed": "events.check_completed",
  "release.found": "events.release_found",
  "download.submitted": "events.download_submitted",
  "download.progress": "events.download_progress",
  "download.completed": "events.download_completed",
  "check.failed": "events.check_failed",
  "session.expired": "events.session_expired",
  // legacy aliases still present on older notifier rows
  updated: "events.release_found",
  error: "events.check_failed",
};
