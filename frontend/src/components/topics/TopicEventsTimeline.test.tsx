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
  it("renders event labels and timestamps without the duplicated topic title", async () => {
    (api.topicEvents as ReturnType<typeof vi.fn>).mockResolvedValue({
      events: [
        { id: 2, event_type: "release.found", severity: "info", message: "Super Mario Galaxy Movie [2026, DCPRip 1080p]", created_at: "2026-06-25T10:00:00Z" },
        { id: 1, event_type: "check.failed", severity: "error", message: "Super Mario Galaxy Movie [2026, DCPRip 1080p]", created_at: "2026-06-25T09:00:00Z" },
      ],
    });
    wrap(<TopicEventsTimeline topicId="t1" />);
    expect(await screen.findByText("new release")).toBeInTheDocument();
    expect(screen.getByText("error")).toBeInTheDocument();
    // The persisted message is the topic's own title — inside a per-topic
    // timeline it must not be rendered (it duplicated on every row).
    expect(screen.queryByText(/Super Mario Galaxy Movie/)).toBeNull();
  });
});
