import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { EventStreamProvider } from "@/components/EventStreamProvider";
import { useSseStatus } from "@/lib/sse-status";

vi.mock("@/lib/api", () => ({
  API_BASE: "/api/v1",
  api: { eventsTicket: vi.fn().mockResolvedValue({ ticket: "tok123" }) },
}));

class MockEventSource {
  static last: MockEventSource | null = null;
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(url: string) {
    this.url = url;
    MockEventSource.last = this;
    queueMicrotask(() => this.onopen?.());
  }
  emit(data: string, lastEventId = "") {
    this.onmessage?.({ data, lastEventId } as MessageEvent);
  }
}

beforeEach(() => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource);
  useSseStatus.setState({ connected: false });
  MockEventSource.last = null;
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
});
