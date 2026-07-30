import { describe, it, expect } from "vitest";

import { mapWithConcurrency } from "./concurrency";

describe("mapWithConcurrency", () => {
  it("never exceeds the limit and preserves input order", async () => {
    let inFlight = 0;
    let peak = 0;
    const items = [10, 20, 30, 40, 50, 60, 70];

    const results = await mapWithConcurrency(items, 3, async (n) => {
      inFlight++;
      peak = Math.max(peak, inFlight);
      // Yield twice so several tasks genuinely overlap.
      await Promise.resolve();
      await Promise.resolve();
      inFlight--;
      return n * 2;
    });

    expect(peak).toBe(3);
    expect(results).toEqual([20, 40, 60, 80, 100, 120, 140]);
  });

  it("runs every item when there are fewer items than the limit", async () => {
    const results = await mapWithConcurrency(["a", "b"], 8, async (s) => s.toUpperCase());
    expect(results).toEqual(["A", "B"]);
  });

  it("resolves to an empty array for an empty input", async () => {
    let calls = 0;
    const results = await mapWithConcurrency([], 4, async () => {
      calls++;
      return 1;
    });
    expect(results).toEqual([]);
    expect(calls).toBe(0);
  });
});
