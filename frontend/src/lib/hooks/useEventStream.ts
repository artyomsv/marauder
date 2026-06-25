import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api, API_BASE } from "@/lib/api";
import { applyEvent, type WireEvent } from "@/lib/events-stream";
import { useSseStatus } from "@/lib/sse-status";

const BACKOFF_START = 1000;
const BACKOFF_MAX = 30000;

// useEventStream maintains a single live SSE connection while mounted. The
// SSE ticket is single-use, so native auto-reconnect can't work — we manage
// reconnection manually (fresh ticket each attempt), and carry the last seen
// event id forward as a query param so replay survives a reconnect.
export function useEventStream(): void {
  const qc = useQueryClient();
  useEffect(() => {
    let stopped = false;
    let es: EventSource | null = null;
    let backoff = BACKOFF_START;
    let lastEventId = "";
    let timer: ReturnType<typeof setTimeout> | undefined;

    const scheduleReconnect = () => {
      if (stopped) return;
      backoff = Math.min(backoff * 2, BACKOFF_MAX);
      timer = setTimeout(connect, backoff);
    };

    async function connect() {
      if (stopped) return;
      let ticket: string;
      try {
        ticket = (await api.eventsTicket()).ticket;
      } catch {
        scheduleReconnect();
        return;
      }
      if (stopped) return;
      let url = `${API_BASE}/events?ticket=${encodeURIComponent(ticket)}`;
      if (lastEventId) url += `&last_event_id=${encodeURIComponent(lastEventId)}`;
      es = new EventSource(url);
      es.onopen = () => {
        backoff = BACKOFF_START;
        useSseStatus.getState().setConnected(true);
      };
      es.onmessage = (e) => {
        if (e.lastEventId) lastEventId = e.lastEventId;
        try {
          applyEvent(qc, JSON.parse(e.data) as WireEvent);
        } catch {
          // ignore malformed frame
        }
      };
      es.onerror = () => {
        useSseStatus.getState().setConnected(false);
        es?.close();
        es = null;
        scheduleReconnect();
      };
    }

    connect();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      es?.close();
      useSseStatus.getState().setConnected(false);
    };
  }, [qc]);
}
