import { create } from "zustand";

export type CheckPhase = "checking" | "idle" | "error";

export interface CheckEntry {
  phase: CheckPhase;
  nextCheckAt?: string;
  error?: string;
}

// Live per-topic check status, fed by check.* SSE events. Drives the topic
// row's "checking…" pulse and next-check countdown.
interface CheckStatusState {
  byTopic: Record<string, CheckEntry>;
  setChecking: (topicId: string) => void;
  setChecked: (topicId: string, nextCheckAt?: string) => void;
  setFailed: (topicId: string, error?: string) => void;
}

export const useCheckStatus = create<CheckStatusState>((set) => ({
  byTopic: {},
  setChecking: (topicId) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "checking" } } })),
  setChecked: (topicId, nextCheckAt) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "idle", nextCheckAt } } })),
  setFailed: (topicId, error) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "error", error } } })),
}));
