import { describe, it, expect } from "vitest";
import {
  NOTIFIABLE_EVENTS,
  ALL_NOTIFIABLE_EVENTS,
  EVENT_LABELS,
  groupConsecutiveEvents,
} from "@/lib/events";
import type { TopicEvent } from "@/lib/api";

// Newest-first, matching what GET /topics/{id}/events returns.
function ev(id: number, type: string, at: string, severity: TopicEvent["severity"] = "info"): TopicEvent {
  return { id, event_type: type, severity, message: "", created_at: at };
}

describe("events catalog", () => {
  it("exposes the four notifiable groups", () => {
    expect(NOTIFIABLE_EVENTS).toContain("release.found");
    expect(NOTIFIABLE_EVENTS).toContain("download.submitted");
    expect(NOTIFIABLE_EVENTS).toContain("download.completed");
    expect(NOTIFIABLE_EVENTS).toContain("check.failed");
  });
  it("keeps session.expired in the flattened default so the errors group still delivers it", () => {
    // Guards the key invariant: the single 'errors' checkbox must persist
    // session.expired, otherwise that alert silently never fires.
    expect(ALL_NOTIFIABLE_EVENTS).toContain("session.expired");
  });
  it("has an i18n label key for every notifiable event", () => {
    for (const e of NOTIFIABLE_EVENTS) {
      expect(EVENT_LABELS[e]).toBeTruthy();
    }
  });
});

describe("groupConsecutiveEvents", () => {
  it("returns nothing for an empty feed", () => {
    expect(groupConsecutiveEvents([])).toEqual([]);
  });

  it("collapses a run of the same event type into one group spanning its time range", () => {
    // The scheduler re-detects a stuck release on every retry tick, so a
    // single release can produce dozens of identical rows.
    const groups = groupConsecutiveEvents([
      ev(3, "release.found", "2026-07-27T15:35:24Z"),
      ev(2, "release.found", "2026-07-27T09:34:23Z"),
      ev(1, "release.found", "2026-07-26T21:32:27Z"),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].count).toBe(3);
    expect(groups[0].newestAt).toBe("2026-07-27T15:35:24Z");
    expect(groups[0].oldestAt).toBe("2026-07-26T21:32:27Z");
  });

  it("keys each group by its newest event id so React reconciles stably", () => {
    const groups = groupConsecutiveEvents([
      ev(9, "release.found", "2026-07-27T15:00:00Z"),
      ev(8, "release.found", "2026-07-27T14:00:00Z"),
    ]);
    expect(groups[0].id).toBe(9);
  });

  it("does not merge same-type runs separated by a different event", () => {
    // Grouping is CONSECUTIVE-only: merging globally would reorder history
    // and hide that a failure happened between two detections.
    const groups = groupConsecutiveEvents([
      ev(4, "release.found", "2026-07-27T15:00:00Z"),
      ev(3, "check.failed", "2026-07-27T14:00:00Z", "error"),
      ev(2, "release.found", "2026-07-27T13:00:00Z"),
      ev(1, "release.found", "2026-07-27T12:00:00Z"),
    ]);
    expect(groups.map((g) => [g.event_type, g.count])).toEqual([
      ["release.found", 1],
      ["check.failed", 1],
      ["release.found", 2],
    ]);
  });

  it("carries the severity of the run so the dot still reflects the events", () => {
    const groups = groupConsecutiveEvents([
      ev(2, "check.failed", "2026-07-27T15:00:00Z", "error"),
      ev(1, "check.failed", "2026-07-27T14:00:00Z", "error"),
    ]);
    expect(groups[0].severity).toBe("error");
  });
});
