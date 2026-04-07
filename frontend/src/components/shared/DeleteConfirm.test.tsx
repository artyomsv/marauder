import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { DeleteConfirm } from "./DeleteConfirm";

// DeleteConfirm has a tiny three-state machine driven by useArmedConfirm:
//   idle  -> click trash      -> armed
//   armed -> click confirm    -> idle + onConfirm fires
//   armed -> click cancel     -> idle, no fire
//   armed -> wait timeoutMs   -> idle, no fire
//
// We use fake timers with `shouldAdvanceTime: true` so vi can drive
// the auto-disarm path deterministically while still letting
// userEvent's internal setTimeout(0) microtask scheduling drain
// without deadlocking against the fake clock.
describe("DeleteConfirm", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // The component uses the `label` prop verbatim as the trash button's
  // aria-label, and `Confirm <label>` / `Cancel <label>` (lowercased)
  // as the inner button labels — see DeleteConfirm.tsx.
  const idleLabel = /^delete topic$/i;
  const confirmLabel = /^confirm delete topic$/i;
  const cancelLabel = /^cancel delete topic$/i;

  it("renders the trash button in idle state without the confirm row", () => {
    render(<DeleteConfirm onConfirm={() => {}} label="Delete topic" />);

    expect(screen.getByRole("button", { name: idleLabel })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: confirmLabel })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: cancelLabel })).not.toBeInTheDocument();
  });

  it("arms on first click, revealing the confirm and cancel buttons", async () => {
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    const onConfirm = vi.fn();
    render(<DeleteConfirm onConfirm={onConfirm} label="Delete topic" />);

    await user.click(screen.getByRole("button", { name: idleLabel }));

    expect(screen.getByRole("button", { name: confirmLabel })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: cancelLabel })).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("fires onConfirm exactly once when the confirm button is clicked", async () => {
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    const onConfirm = vi.fn();
    render(<DeleteConfirm onConfirm={onConfirm} label="Delete topic" />);

    await user.click(screen.getByRole("button", { name: idleLabel }));
    await user.click(screen.getByRole("button", { name: confirmLabel }));

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("returns to idle when cancel is clicked, without firing onConfirm", async () => {
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    const onConfirm = vi.fn();
    render(<DeleteConfirm onConfirm={onConfirm} label="Delete topic" />);

    await user.click(screen.getByRole("button", { name: idleLabel }));
    await user.click(screen.getByRole("button", { name: cancelLabel }));

    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: idleLabel })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: confirmLabel })).not.toBeInTheDocument();
  });

  it("auto-disarms after the default 4-second timeout", async () => {
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    const onConfirm = vi.fn();
    render(<DeleteConfirm onConfirm={onConfirm} label="Delete topic" />);

    await user.click(screen.getByRole("button", { name: idleLabel }));
    expect(screen.getByRole("button", { name: confirmLabel })).toBeInTheDocument();

    // Drive the auto-disarm timer and let React flush the resulting
    // state update inside the same act() batch.
    act(() => {
      vi.advanceTimersByTime(4000);
    });

    expect(screen.queryByRole("button", { name: confirmLabel })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: idleLabel })).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("respects a custom timeoutSeconds value", async () => {
    const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) });
    const customIdle = /^delete client$/i;
    const customConfirm = /^confirm delete client$/i;
    render(
      <DeleteConfirm onConfirm={() => {}} label="Delete client" timeoutSeconds={2} />,
    );

    await user.click(screen.getByRole("button", { name: customIdle }));
    expect(screen.getByRole("button", { name: customConfirm })).toBeInTheDocument();

    // Halfway through the custom window — still armed.
    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(screen.getByRole("button", { name: customConfirm })).toBeInTheDocument();

    // Cross the 2-second threshold — should disarm.
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(screen.queryByRole("button", { name: customConfirm })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: customIdle })).toBeInTheDocument();
  });

  it("disables the trash button when isPending is true", () => {
    render(<DeleteConfirm onConfirm={() => {}} label="Delete topic" isPending />);

    expect(screen.getByRole("button", { name: idleLabel })).toBeDisabled();
  });
});
