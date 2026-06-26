import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { useCheckStatus, CHECKING_STALE_MS } from "@/lib/check-status";

describe("useCheckStatus", () => {
  beforeEach(() => useCheckStatus.setState({ byTopic: {} }));
  afterEach(() => vi.useRealTimers());

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

  it("clear removes the entry for the given topic", () => {
    useCheckStatus.getState().setChecking("t3");
    expect(useCheckStatus.getState().byTopic["t3"]).toBeDefined();
    useCheckStatus.getState().clear("t3");
    expect(useCheckStatus.getState().byTopic["t3"]).toBeUndefined();
  });

  it("auto-reverts a stuck 'checking' to idle after the staleness window", () => {
    vi.useFakeTimers();
    useCheckStatus.getState().setChecking("t4");
    expect(useCheckStatus.getState().byTopic["t4"].phase).toBe("checking");
    // A missed check.completed (ephemeral, not replayed) would spin forever.
    vi.advanceTimersByTime(CHECKING_STALE_MS + 1);
    expect(useCheckStatus.getState().byTopic["t4"].phase).toBe("idle");
  });

  it("a terminal event cancels the staleness timer (no spurious revert)", () => {
    vi.useFakeTimers();
    useCheckStatus.getState().setChecking("t5");
    useCheckStatus.getState().setFailed("t5", "boom");
    vi.advanceTimersByTime(CHECKING_STALE_MS + 1);
    const e = useCheckStatus.getState().byTopic["t5"];
    expect(e.phase).toBe("error");
    expect(e.error).toBe("boom");
  });
});
