import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

// AddTopicCard pulls in TopicForm (clients/credentials/match/preview
// queries) and TrackerSearch (search query) — mock the whole api surface
// they touch.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      previewTracker: vi.fn(),
      getClientCategories: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";
import { AddTopicCard } from "./AddTopicCard";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  post: ReturnType<typeof vi.fn>;
  previewTracker: ReturnType<typeof vi.fn>;
  getClientCategories: ReturnType<typeof vi.fn>;
};

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const SEARCH_RESULTS = {
  results: [
    {
      tracker_name: "rutor",
      tracker_display_name: "Rutor.org",
      title: "Picked release",
      url: "https://rutor.org/torrent/975045",
      size: "1.4 GB",
      seeders: 17,
    },
  ],
  errors: [],
};

beforeEach(() => {
  mockApi.get.mockReset();
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/search")) return Promise.resolve(SEARCH_RESULTS);
    if (path.startsWith("/trackers/match"))
      return Promise.resolve({
        tracker_name: "rutor",
        display_name: "Rutor.org",
        supports_episode_filter: false,
        supports_season_catalog: false,
        requires_credentials: false,
        credentials_optional: false,
        uses_cloudflare: false,
      });
    if (path.startsWith("/clients")) return Promise.resolve({ clients: [] });
    if (path.startsWith("/credentials")) return Promise.resolve({ credentials: [] });
    return Promise.resolve({});
  });
  mockApi.previewTracker.mockResolvedValue({ title: "", image_url: "" });
  mockApi.getClientCategories.mockResolvedValue({ supported: false, categories: [] });
});

describe("AddTopicCard", () => {
  it("opens in URL mode with the topic form visible", () => {
    render(<AddTopicCard onClose={() => {}} onCreated={() => {}} />, {
      wrapper: wrap(),
    });
    expect(screen.getByLabelText(/url or magnet link/i)).toBeInTheDocument();
    expect(
      screen.queryByPlaceholderText(/search releases across your trackers/i),
    ).toBeNull();
  });

  it("switches to search mode via the tab", async () => {
    render(<AddTopicCard onClose={() => {}} onCreated={() => {}} />, {
      wrapper: wrap(),
    });
    await userEvent.click(screen.getByRole("button", { name: /search trackers/i }));
    expect(
      screen.getByPlaceholderText(/search releases across your trackers/i),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText(/url or magnet link/i)).toBeNull();
  });

  it("prefills the URL form when a search result is picked", async () => {
    render(<AddTopicCard onClose={() => {}} onCreated={() => {}} />, {
      wrapper: wrap(),
    });
    await userEvent.click(screen.getByRole("button", { name: /search trackers/i }));
    await userEvent.type(
      screen.getByPlaceholderText(/search releases across your trackers/i),
      "picked",
    );
    await userEvent.click(screen.getByRole("button", { name: /^search$/i }));

    await userEvent.click(await screen.findByText("Picked release"));

    // Back in URL mode, form remounted with the picked URL prefilled.
    const urlInput = screen.getByLabelText(/url or magnet link/i);
    expect(urlInput).toHaveValue("https://rutor.org/torrent/975045");
  });
});
