import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TopicEventsTimeline } from "@/components/topics/TopicEventsTimeline";
import { api } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  api: { topicEvents: vi.fn() },
}));

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("TopicEventsTimeline", () => {
  beforeEach(() => vi.clearAllMocks());
  it("renders events newest-first with labels", async () => {
    (api.topicEvents as ReturnType<typeof vi.fn>).mockResolvedValue({
      events: [
        { id: 2, event_type: "release.found", severity: "info", message: "New release", created_at: "2026-06-25T10:00:00Z" },
        { id: 1, event_type: "check.failed", severity: "error", message: "boom", created_at: "2026-06-25T09:00:00Z" },
      ],
    });
    wrap(<TopicEventsTimeline topicId="t1" />);
    expect(await screen.findByText("New release")).toBeInTheDocument();
    expect(screen.getByText("boom")).toBeInTheDocument();
  });
});
