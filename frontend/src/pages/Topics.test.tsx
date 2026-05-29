import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

import { AddTopicCard } from "./Topics";
import { ApiError } from "@/lib/api";

// AddTopicCard talks to the backend exclusively through the `api` object,
// so mocking `api.get` is enough to exercise the season/episode catalog
// rendering without a network or a running backend.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      del: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  post: ReturnType<typeof vi.fn>;
};

const LOSTFILM_URL = "https://www.lostfilm.tv/series/Some_Show/seasons";

const catalogMatch = {
  tracker_name: "lostfilm",
  display_name: "LostFilm",
  supports_episode_filter: true,
  supports_season_catalog: true,
  requires_credentials: false,
  uses_cloudflare: false,
};

// Routes /trackers/match and /trackers/seasons; everything else (e.g. the
// /credentials list the card also reads) resolves empty.
function routeGet(seasonsImpl: () => unknown) {
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match")) return Promise.resolve(catalogMatch);
    if (path.startsWith("/trackers/seasons")) return seasonsImpl();
    return Promise.resolve({ credentials: [] });
  });
}

function renderCard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return render(<AddTopicCard onClose={() => {}} onCreated={() => {}} />, {
    wrapper,
  });
}

describe("AddTopicCard — season/episode catalog dropdowns", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders season options from the catalog and populates episodes on selection", async () => {
    const user = userEvent.setup();
    routeGet(() =>
      Promise.resolve({
        seasons: [
          { number: 1, episodes: [1, 2] },
          { number: 2, episodes: [1, 2, 3] },
        ],
      }),
    );

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    // Wait for the catalog-specific Season option to appear — the label
    // "Start from season" is shared with the free-text fallback, so we key
    // off the dropdown's option to know the catalog rendered.
    await screen.findByRole("option", { name: "Season 1" });
    const seasonSelect = screen.getByLabelText(/start from season/i) as HTMLSelectElement;
    expect(seasonSelect.tagName).toBe("SELECT");
    expect(within(seasonSelect).getByRole("option", { name: "Season 1" })).toBeInTheDocument();
    expect(within(seasonSelect).getByRole("option", { name: "Season 2" })).toBeInTheDocument();

    // Episode dropdown is disabled until a season is chosen.
    const episodeSelect = screen.getByLabelText(/start from episode/i);
    expect(episodeSelect).toBeDisabled();

    // Selecting Season 2 populates exactly its 3 episodes.
    await user.selectOptions(seasonSelect, "2");
    expect(episodeSelect).not.toBeDisabled();
    const episodeOptions = within(episodeSelect)
      .getAllByRole("option")
      .map((o) => o.textContent);
    // "From the start" + Episode 1/2/3.
    expect(episodeOptions).toEqual(["From the start", "Episode 1", "Episode 2", "Episode 3"]);
  });

  it("falls back to free-text number inputs when the seasons query errors", async () => {
    const user = userEvent.setup();
    routeGet(() =>
      Promise.reject(new ApiError({ title: "Unprocessable", status: 422 })),
    );

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    // Match resolved → the episode-filter block renders. Because the catalog
    // fetch errored, the fields must be the free-text number inputs.
    const seasonInput = await screen.findByLabelText(/start from season/i);
    expect(seasonInput.tagName).toBe("INPUT");
    expect(seasonInput).toHaveAttribute("type", "number");

    const episodeInput = screen.getByLabelText(/start from episode/i);
    expect(episodeInput.tagName).toBe("INPUT");
    expect(episodeInput).toHaveAttribute("type", "number");
  });
});
