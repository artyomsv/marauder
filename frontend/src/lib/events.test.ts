import { describe, it, expect } from "vitest";
import { NOTIFIABLE_EVENTS, EVENT_LABELS } from "@/lib/events";

describe("events catalog", () => {
  it("exposes the four notifiable groups", () => {
    expect(NOTIFIABLE_EVENTS).toContain("release.found");
    expect(NOTIFIABLE_EVENTS).toContain("download.submitted");
    expect(NOTIFIABLE_EVENTS).toContain("download.completed");
    expect(NOTIFIABLE_EVENTS).toContain("check.failed");
  });
  it("has an i18n label key for every notifiable event", () => {
    for (const e of NOTIFIABLE_EVENTS) {
      expect(EVENT_LABELS[e]).toBeTruthy();
    }
  });
});
