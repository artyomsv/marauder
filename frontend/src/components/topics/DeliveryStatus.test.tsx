import { describe, it, expect } from "vitest";
import { pollInterval, ACTIVE_POLL_MS, IDLE_POLL_MS } from "@/components/topics/DeliveryStatus";

describe("pollInterval", () => {
  const downloading = [{ state: "downloading" }];
  const idle = [{ state: "seeding" }];

  it("disables polling while SSE is connected", () => {
    expect(pollInterval(true, downloading)).toBe(false);
    expect(pollInterval(true, idle)).toBe(false);
  });

  it("polls fast when disconnected and a delivery is active", () => {
    expect(pollInterval(false, downloading)).toBe(ACTIVE_POLL_MS);
  });

  it("polls slow when disconnected and nothing is active", () => {
    expect(pollInterval(false, idle)).toBe(IDLE_POLL_MS);
  });
});
