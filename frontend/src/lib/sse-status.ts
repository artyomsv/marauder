import { create } from "zustand";

// Tracks whether the live SSE connection is up, so views can disable
// polling fallbacks while events are streaming.
interface SseStatusState {
  connected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useSseStatus = create<SseStatusState>((set) => ({
  connected: false,
  setConnected: (connected) => set({ connected }),
}));
