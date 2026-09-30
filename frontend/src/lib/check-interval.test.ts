import { describe, expect, it } from "vitest";

import { CHECK_INTERVAL_PRESETS, intervalParts } from "./check-interval";

describe("intervalParts", () => {
  it.each([
    [900, "minutes", 15],
    [1800, "minutes", 30],
    [3600, "hours", 1],
    [86400, "hours", 24],
    [259200, "days", 3],
    [604800, "days", 7],
    // A value set through the API that is not a whole minute still renders.
    [90, "seconds", 90],
  ])("describes %i seconds as %s × %i", (sec, unit, count) => {
    expect(intervalParts(sec)).toEqual({ unit, count });
  });

  it("offers presets inside the backend's allowed range", () => {
    for (const sec of CHECK_INTERVAL_PRESETS) {
      expect(sec).toBeGreaterThanOrEqual(300);
      expect(sec).toBeLessThanOrEqual(604800);
    }
  });
});
