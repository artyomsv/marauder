import type { QueryClient } from "@tanstack/react-query";

import { QK } from "@/lib/queryKeys";
import { useCheckStatus } from "@/lib/check-status";
import type { TopicStatus } from "@/lib/api";

// WireEvent mirrors the backend sse.wireEvent JSON in each `data:` line.
export interface WireEvent {
  id: number;
  type: string;
  topic_id?: string;
  severity?: string;
  title?: string;
  body?: string;
  link?: string;
  data?: Record<string, unknown>;
}

const PERSISTED = new Set([
  "topic.added", "topic.reset", "release.found", "download.submitted",
  "download.completed", "check.failed", "session.expired",
]);

// applyEvent routes one live event into the React Query cache / check store.
// Pure w.r.t. its inputs (no network); safe to unit-test.
export function applyEvent(qc: QueryClient, ev: WireEvent): void {
  const topicId = ev.topic_id;
  if (topicId && PERSISTED.has(ev.type)) {
    qc.invalidateQueries({ queryKey: QK.topicEvents(topicId) });
  }
  switch (ev.type) {
    case "download.progress": {
      // Progress events carry {infohash, percent_done, state} — patch in place.
      if (topicId) patchDeliveryProgress(qc, topicId, ev);
      return;
    }
    case "download.completed": {
      // The completed event carries no infohash in Data (only title/body), so
      // there's nothing to patch by hash — refetch the topic's status to pick
      // up the finished delivery.
      if (topicId) qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
      return;
    }
    case "check.started":
      if (topicId) useCheckStatus.getState().setChecking(topicId);
      return;
    case "check.completed":
      if (topicId) useCheckStatus.getState().setChecked(topicId, ev.data?.next_check_at as string | undefined);
      return;
    case "check.failed":
      if (topicId) useCheckStatus.getState().setFailed(topicId, ev.body);
      return;
    case "release.found":
    case "download.submitted":
    case "topic.reset":
      if (topicId) qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
      qc.invalidateQueries({ queryKey: QK.topics });
      return;
    case "topic.added":
      qc.invalidateQueries({ queryKey: QK.topics });
      return;
    default:
      return;
  }
}

function patchDeliveryProgress(qc: QueryClient, topicId: string, ev: WireEvent): void {
  const infohash = (ev.data?.infohash as string | undefined)?.toLowerCase();
  if (!infohash) return;
  const cached = qc.getQueryData<TopicStatus>(QK.topicStatus(topicId));
  if (!cached) {
    // No cached deliveries yet (e.g. a brand-new one) — refetch to pick it up.
    qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
    return;
  }
  const percent = ev.data?.percent_done as number | undefined;
  const state = ev.data?.state as string | undefined;
  qc.setQueryData<TopicStatus>(QK.topicStatus(topicId), {
    ...cached,
    deliveries: cached.deliveries.map((d) =>
      d.infohash.toLowerCase() === infohash
        ? { ...d, percent_done: percent ?? d.percent_done, state: state ?? d.state }
        : d,
    ),
  });
}
