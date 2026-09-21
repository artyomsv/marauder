import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { TopicDiagnosticsCard } from "@/components/topics/TopicDiagnosticsCard";
import { api, type Topic, type TopicDiagnosticsPage } from "@/lib/api";

const topic = { ID: "t-1", DisplayName: "Some Show" } as Topic;

const page: TopicDiagnosticsPage = {
  tracker: "tapochek",
  url: "https://tapochek.net/viewtopic.php?t=288010",
  authenticated: true,
  bytes: 109961,
  fetched_at: "2026-09-21T15:00:00Z",
  redacted: true,
  redaction_mark: "MARAUDER-REDACTED",
  html: "<th class=\"seedmed\">Some.Release.torrent</th>",
};

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TopicDiagnosticsCard topic={topic} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

describe("TopicDiagnosticsCard", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches on demand rather than on mount", async () => {
    const spy = vi.spyOn(api, "topicDiagnosticsPage").mockResolvedValue(page);
    renderCard();
    // The request hits the user's tracker with their credentials. Opening the
    // card must not be enough to trigger that.
    expect(spy).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: /fetch page/i }));
    expect(spy).toHaveBeenCalledWith("t-1");
    // The size and tracker render as sibling text nodes, so match on the
    // element's combined text rather than on one node.
    expect(
      await screen.findByText((_, el) => el?.textContent === "tapochek · 107 KB"),
    ).toBeInTheDocument();
  });

  it("shows the redaction warning and the marker to look for", async () => {
    vi.spyOn(api, "topicDiagnosticsPage").mockResolvedValue(page);
    renderCard();
    await userEvent.click(screen.getByRole("button", { name: /fetch page/i }));

    // The warning is the point, not decoration: redaction removes the secrets
    // we know about on a page we do not control, and the user is about to post
    // it in public.
    expect(await screen.findByText(/skim the file before you post it/i)).toBeInTheDocument();
    expect(screen.getByText("MARAUDER-REDACTED")).toBeInTheDocument();
  });

  it("says so when the page was fetched without an account", async () => {
    vi.spyOn(api, "topicDiagnosticsPage").mockResolvedValue({
      ...page,
      authenticated: false,
    });
    renderCard();
    await userEvent.click(screen.getByRole("button", { name: /fetch page/i }));
    expect(await screen.findByText(/fetched without an account/i)).toBeInTheDocument();
  });

  it("surfaces a failed fetch instead of an empty card", async () => {
    vi.spyOn(api, "topicDiagnosticsPage").mockRejectedValue(
      new Error("tapochek GET /viewtopic.php -> 503"),
    );
    renderCard();
    await userEvent.click(screen.getByRole("button", { name: /fetch page/i }));
    expect(await screen.findByText(/503/)).toBeInTheDocument();
  });
});
