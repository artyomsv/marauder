import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { ResetTopicCard } from "./ResetTopicCard";
import { api, type Topic } from "@/lib/api";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { ...actual.api, resetTopic: vi.fn() } };
});

const topic = (id: string, name: string) => ({ ID: id, DisplayName: name }) as Topic;

function renderCard(topics: Topic[], onDone = vi.fn(), onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ResetTopicCard topics={topics} onClose={onClose} onDone={onDone} />
    </QueryClientProvider>,
  );
  return { onDone, onClose };
}

describe("ResetTopicCard", () => {
  beforeEach(() => {
    vi.mocked(api.resetTopic).mockReset();
  });

  it("defaults the delete-data checkbox to off and forwards its value", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({ removed: 1, warnings: [] });
    renderCard([topic("t1", "Show")]);

    const checkbox = screen.getByRole("checkbox");
    expect(checkbox).not.toBeChecked();

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));
    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledWith("t1", false));
  });

  it("forwards delete_data when the checkbox is ticked", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({ removed: 1, warnings: [] });
    renderCard([topic("t1", "Show")]);

    await userEvent.click(screen.getByRole("checkbox"));
    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledWith("t1", true));
  });

  it("shows the result and its warnings without auto-closing", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({
      removed: 2,
      warnings: ["transmission: removal failed: connection refused"],
    });
    const { onClose } = renderCard([topic("t1", "Show")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    expect(await screen.findByText(/removed 2 torrent/i)).toBeInTheDocument();
    expect(screen.getByText(/connection refused/i)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("resets every selected topic and aggregates the results", async () => {
    vi.mocked(api.resetTopic)
      .mockResolvedValueOnce({ removed: 1, warnings: [] })
      .mockResolvedValueOnce({ removed: 2, warnings: [] });
    const { onDone } = renderCard([topic("t1", "A"), topic("t2", "B")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(api.resetTopic).toHaveBeenCalledTimes(2));
    expect(await screen.findByText(/removed 3 torrent/i)).toBeInTheDocument();
    expect(onDone).toHaveBeenCalled();
  });

  it("turns a failed topic into a warning instead of losing the others", async () => {
    vi.mocked(api.resetTopic)
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce({ removed: 1, warnings: [] });
    renderCard([topic("t1", "A"), topic("t2", "B")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    expect(await screen.findByText(/A: boom/)).toBeInTheDocument();
    expect(screen.getByText(/removed 1 torrent/i)).toBeInTheDocument();
  });

  it("suppresses the success headline when every topic failed", async () => {
    vi.mocked(api.resetTopic)
      .mockRejectedValueOnce(new Error("boom"))
      .mockRejectedValueOnce(new Error("bang"));
    renderCard([topic("t1", "A"), topic("t2", "B")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    expect(await screen.findByText(/A: boom/)).toBeInTheDocument();
    expect(screen.getByText(/B: bang/)).toBeInTheDocument();
    // "Removed 0 torrent(s). Queued for a fresh check." directly above two
    // warnings saying nothing was reset is a flat contradiction.
    expect(screen.queryByText(/queued for a fresh check/i)).not.toBeInTheDocument();
  });

  it("keeps the success headline when a topic succeeded with warnings only", async () => {
    vi.mocked(api.resetTopic).mockResolvedValue({
      removed: 0,
      warnings: ["qbittorrent: this client cannot remove torrents"],
    });
    renderCard([topic("t1", "A")]);

    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));

    // Client removal is fail-open — the topic was still queued for a re-check,
    // so the headline is accurate even at removed=0.
    expect(await screen.findByText(/queued for a fresh check/i)).toBeInTheDocument();
    expect(screen.getByText(/cannot remove torrents/i)).toBeInTheDocument();
  });
});
