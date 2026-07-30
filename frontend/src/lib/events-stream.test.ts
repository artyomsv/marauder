import { describe, it, expect, beforeEach, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { applyEvent } from "@/lib/events-stream";
import { QK } from "@/lib/queryKeys";
import { useCheckStatus } from "@/lib/check-status";
import type { TopicStatus } from "@/lib/api";

describe("applyEvent", () => {
  beforeEach(() => useCheckStatus.setState({ byTopic: {} }));

  it("patches topicStatus cache on download.progress by infohash", () => {
    const qc = new QueryClient();
    const seed: TopicStatus = {
      client_supports_status: true,
      deliveries: [{ label: "s01e01", infohash: "ABC", delivered_at: "x", state: "downloading", percent_done: 0.1 }],
    };
    qc.setQueryData(QK.topicStatus("t1"), seed);
    applyEvent(qc, {
      id: 0, type: "download.progress", topic_id: "t1",
      data: { infohash: "abc", percent_done: 0.6, state: "downloading" },
    });
    const got = qc.getQueryData<TopicStatus>(QK.topicStatus("t1"));
    expect(got?.deliveries[0].percent_done).toBe(0.6);
  });

  it("updates check store on check.completed with next_check_at", () => {
    const qc = new QueryClient();
    applyEvent(qc, { id: 7, type: "check.completed", topic_id: "t1", data: { next_check_at: "2026-06-26T10:00:00Z" } });
    expect(useCheckStatus.getState().byTopic["t1"].nextCheckAt).toBe("2026-06-26T10:00:00Z");
  });

  it("invalidates topics list on release.found", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 3, type: "release.found", topic_id: "t1" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topics });
  });

  it("refetches topic status on download.completed (no infohash to patch)", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 9, type: "download.completed", topic_id: "t1" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicStatus("t1") });
  });

  it("invalidates topicStatus when download.progress arrives and no cache is seeded", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, {
      id: 10, type: "download.progress", topic_id: "t1",
      data: { infohash: "aabbcc", percent_done: 0.5, state: "downloading" },
    });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicStatus("t1") });
  });

  it("sets check store to error phase on check.failed", () => {
    const qc = new QueryClient();
    applyEvent(qc, { id: 11, type: "check.failed", topic_id: "t2", body: "timeout" });
    const entry = useCheckStatus.getState().byTopic["t2"];
    expect(entry.phase).toBe("error");
    expect(entry.error).toBe("timeout");
  });

  it("invalidates topicEvents on a persisted event (release.found)", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 12, type: "release.found", topic_id: "t3" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicEvents("t3") });
  });

  it("clears the check store on topic.reset", () => {
    const qc = new QueryClient();
    // A tab that did not perform the reset only learns about it over SSE, so
    // without this its check chip keeps showing the pre-reset error.
    applyEvent(qc, { id: 13, type: "check.failed", topic_id: "t4", body: "timeout" });
    expect(useCheckStatus.getState().byTopic["t4"].phase).toBe("error");

    applyEvent(qc, { id: 14, type: "topic.reset", topic_id: "t4" });
    expect(useCheckStatus.getState().byTopic["t4"]).toBeUndefined();
  });

  it("still invalidates the topic caches on topic.reset", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 15, type: "topic.reset", topic_id: "t5" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicStatus("t5") });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topics });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicEvents("t5") });
  });
});
