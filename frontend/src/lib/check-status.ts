import { create } from "zustand";

export type CheckPhase = "checking" | "idle" | "error";

export interface CheckEntry {
  phase: CheckPhase;
  nextCheckAt?: string;
  error?: string;
}

// A "checking" pulse is driven by the ephemeral check.started event, cleared
// by check.completed / check.failed. Those terminal events carry no SSE id and
// are NOT replayed on reconnect, so if one is missed across an SSE gap (logout,
// network blip, tab sleep, drop-on-full) the spinner would spin forever. Guard
// against that: a check completes in seconds, so auto-revert to idle after this
// long — anything longer is a missed terminal event, not a live check.
export const CHECKING_STALE_MS = 90_000;

// Live per-topic check status, fed by check.* SSE events. Drives the topic
// row's "checking…" pulse and next-check display.
interface CheckStatusState {
  byTopic: Record<string, CheckEntry>;
  setChecking: (topicId: string) => void;
  setChecked: (topicId: string, nextCheckAt?: string) => void;
  setFailed: (topicId: string, error?: string) => void;
  clear: (topicId: string) => void;
  // Drain all entries + their staleness timers — called on SSE teardown
  // (logout) so a fresh session never shows a stale "checking" pulse.
  clearAll: () => void;
}

// Per-topic staleness timers live outside the store (they aren't render state).
const staleTimers: Record<string, ReturnType<typeof setTimeout>> = {};
function cancelStale(topicId: string): void {
  const t = staleTimers[topicId];
  if (t !== undefined) {
    clearTimeout(t);
    delete staleTimers[topicId];
  }
}

export const useCheckStatus = create<CheckStatusState>((set, get) => ({
  byTopic: {},
  setChecking: (topicId) => {
    cancelStale(topicId);
    staleTimers[topicId] = setTimeout(() => {
      delete staleTimers[topicId];
      // Only clear if still stuck "checking" — a terminal event would have
      // replaced the entry and cancelled this timer.
      const cur = get().byTopic[topicId];
      if (cur?.phase === "checking") {
        set((s) => ({
          byTopic: { ...s.byTopic, [topicId]: { phase: "idle", nextCheckAt: cur.nextCheckAt } },
        }));
      }
    }, CHECKING_STALE_MS);
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "checking" } } }));
  },
  setChecked: (topicId, nextCheckAt) => {
    cancelStale(topicId);
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "idle", nextCheckAt } } }));
  },
  setFailed: (topicId, error) => {
    cancelStale(topicId);
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "error", error } } }));
  },
  clear: (topicId) => {
    cancelStale(topicId);
    set((s) => {
      const { [topicId]: _omit, ...rest } = s.byTopic;
      return { byTopic: rest };
    });
  },
  clearAll: () => {
    for (const id of Object.keys(staleTimers)) cancelStale(id);
    set({ byTopic: {} });
  },
}));
