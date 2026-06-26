import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { EventStreamProvider } from "@/components/EventStreamProvider";
import { useSseStatus } from "@/lib/sse-status";

vi.mock("@/lib/api", () => ({
  API_BASE: "/api/v1",
  api: { eventsTicket: vi.fn().mockResolvedValue({ ticket: "tok123" }) },
}));

class MockEventSource {
  static last: MockEventSource | null = null;
  static instances: MockEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(url: string) {
    this.url = url;
    MockEventSource.last = this;
    MockEventSource.instances.push(this);
    queueMicrotask(() => this.onopen?.());
  }
  emit(data: string, lastEventId = "") {
    this.onmessage?.({ data, lastEventId } as MessageEvent);
  }
  triggerError() {
    this.onerror?.();
  }
}

beforeEach(() => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource);
  useSseStatus.setState({ connected: false });
  MockEventSource.last = null;
  MockEventSource.instances = [];
});

afterEach(() => {
  vi.useRealTimers();
});

function wrap() {
  const qc = new QueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <EventStreamProvider>
        <div>child</div>
      </EventStreamProvider>
    </QueryClientProvider>,
  );
}

describe("EventStreamProvider", () => {
  it("fetches a ticket, opens EventSource with it, and marks connected", async () => {
    wrap();
    await waitFor(() => expect(MockEventSource.last).not.toBeNull());
    expect(MockEventSource.last!.url).toContain("/api/v1/events?ticket=tok123");
    await waitFor(() => expect(useSseStatus.getState().connected).toBe(true));
  });

  it("renders its children", async () => {
    const { getByText } = wrap();
    expect(getByText("child")).toBeInTheDocument();
  });

  it("closes and reconnects after onerror, constructing a second EventSource", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    wrap();
    // Wait for the first EventSource to be created (microtasks for ticket fetch + onopen)
    await vi.runAllTimersAsync();
    await waitFor(() => expect(MockEventSource.instances.length).toBe(1));
    const first = MockEventSource.instances[0];
    // Trigger error — hook will schedule reconnect after 2s (backoff 1000→2000)
    await act(() => { first.triggerError(); });
    expect(first.close).toHaveBeenCalled();
    // Advance past the backoff (2000ms) and flush async work
    await vi.advanceTimersByTimeAsync(3000);
    await waitFor(() => expect(MockEventSource.instances.length).toBe(2));
  });

  it("carries lastEventId forward in the reconnect URL", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    wrap();
    await vi.runAllTimersAsync();
    await waitFor(() => expect(MockEventSource.instances.length).toBe(1));
    const first = MockEventSource.instances[0];
    // Emit a message with a non-empty lastEventId
    await act(() => { first.emit('{"id":5,"type":"check.started","topic_id":"t1"}', "evt-42"); });
    // Trigger reconnect
    await act(() => { first.triggerError(); });
    await vi.advanceTimersByTimeAsync(3000);
    await waitFor(() => expect(MockEventSource.instances.length).toBe(2));
    expect(MockEventSource.instances[1].url).toContain("last_event_id=evt-42");
  });

  it("ignores malformed JSON without disconnecting", async () => {
    wrap();
    await waitFor(() => expect(MockEventSource.instances.length).toBe(1));
    await waitFor(() => expect(useSseStatus.getState().connected).toBe(true));
    const first = MockEventSource.instances[0];
    // Emit non-JSON — should not throw and connection should stay open
    await act(() => { first.emit("not json"); });
    expect(useSseStatus.getState().connected).toBe(true);
    expect(first.close).not.toHaveBeenCalled();
  });
});
