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

// One collapsed run of consecutive same-type events in the history timeline.
// `newestAt`/`oldestAt` are equal when the run holds a single event.
export interface EventGroup {
  // The newest event's id — a stable React key that survives newer events
  // being prepended to the feed.
  id: number;
  event_type: string;
  severity: string;
  count: number;
  newestAt: string;
  oldestAt: string;
}

// The minimum an event needs to be groupable. Kept structural rather than
// importing TopicEvent so the helper stays usable for any event-shaped feed.
interface GroupableEvent {
  id: number;
  event_type: string;
  severity: string;
  created_at: string;
}

// Collapse runs of CONSECUTIVE same-type events into one entry each.
//
// The scheduler re-detects a stuck release on every retry tick, so a single
// unreachable client can push dozens of identical release.found rows into a
// topic's history. Rendering one row per event turns the timeline into a wall
// of duplicates that buries the events that actually differ.
//
// Grouping is deliberately consecutive-only: merging every same-type event in
// the feed would reorder history and hide that, say, a check.failed happened
// between two detections. Input is expected newest-first (the order the API
// returns); the output preserves it.
export function groupConsecutiveEvents(events: GroupableEvent[]): EventGroup[] {
  const groups: EventGroup[] = [];
  for (const e of events) {
    const open = groups[groups.length - 1];
    if (open && open.event_type === e.event_type) {
      open.count += 1;
      open.oldestAt = e.created_at;
      continue;
    }
    groups.push({
      id: e.id,
      event_type: e.event_type,
      severity: e.severity,
      count: 1,
      newestAt: e.created_at,
      oldestAt: e.created_at,
    });
  }
  return groups;
}

// i18n keys for each event label, used by the notifier picker and the
// per-topic timeline. Timeline-only (non-notifiable) events are included so
// the history view can render them too.
export const EVENT_LABELS: Record<string, string> = {
  "topic.added": "events.topic_added",
  "topic.reset": "events.topic_reset",
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
