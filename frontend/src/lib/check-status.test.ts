import { describe, it, expect, beforeEach } from "vitest";
import { useCheckStatus } from "@/lib/check-status";

describe("useCheckStatus", () => {
  beforeEach(() => useCheckStatus.setState({ byTopic: {} }));

  it("tracks checking → checked with next_check_at", () => {
    useCheckStatus.getState().setChecking("t1");
    expect(useCheckStatus.getState().byTopic["t1"].phase).toBe("checking");
    useCheckStatus.getState().setChecked("t1", "2026-06-26T10:00:00Z");
    const e = useCheckStatus.getState().byTopic["t1"];
    expect(e.phase).toBe("idle");
    expect(e.nextCheckAt).toBe("2026-06-26T10:00:00Z");
  });

  it("records a failure with its message", () => {
    useCheckStatus.getState().setFailed("t2", "boom");
    const e = useCheckStatus.getState().byTopic["t2"];
    expect(e.phase).toBe("error");
    expect(e.error).toBe("boom");
  });
});
