import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

import { AddTopicCard } from "./Topics";
import { EditTopicCard } from "@/components/topics/EditTopicCard";
import { ApiError, type Topic } from "@/lib/api";

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
      updateTopic: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  post: ReturnType<typeof vi.fn>;
  updateTopic: ReturnType<typeof vi.fn>;
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

// Two clients the picker should list, plus the "Use default client" option.
const clientsList = {
  clients: [
    { id: "c1", display_name: "qBittorrent" },
    { id: "c2", display_name: "Transmission" },
  ],
};

// Routes /trackers/match, /trackers/seasons and /clients; everything else
// (e.g. the /credentials list the card also reads) resolves empty.
function routeGet(seasonsImpl: () => unknown) {
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match")) return Promise.resolve(catalogMatch);
    if (path.startsWith("/trackers/seasons")) return seasonsImpl();
    if (path.startsWith("/clients")) return Promise.resolve(clientsList);
    return Promise.resolve({ credentials: [] });
  });
}

function wrapperWithClient() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

function renderCard() {
  return render(<AddTopicCard onClose={() => {}} onCreated={() => {}} />, {
    wrapper: wrapperWithClient(),
  });
}

// A listed topic as the backend serializes domain.Topic: PascalCase
// top-level fields, lowercase keys inside the Extra blob.
const EXISTING_TOPIC: Topic = {
  ID: "t-1",
  UserID: "u-1",
  TrackerName: "lostfilm",
  URL: LOSTFILM_URL,
  DisplayName: "Some Show",
  ClientID: "c2",
  DownloadDir: "/downloads/shows",
  Category: "series",
  Extra: { quality: "1080p", start_season: 2, start_episode: 3 },
  LastHash: "",
  LastCheckedAt: null,
  LastUpdatedAt: null,
  NextCheckAt: "",
  CheckIntervalSec: 900,
  ConsecutiveErrors: 0,
  Status: "active",
  LastError: "",
  CreatedAt: "",
  UpdatedAt: "",
};

function renderEdit(topic: Topic = EXISTING_TOPIC) {
  const onSaved = vi.fn();
  render(<EditTopicCard topic={topic} onClose={() => {}} onSaved={onSaved} />, {
    wrapper: wrapperWithClient(),
  });
  return { onSaved };
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

describe("AddTopicCard — client picker + download folder + category", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists the user's clients plus a default option", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    const clientSelect = (await screen.findByLabelText(
      /^client \(optional\)$/i,
    )) as HTMLSelectElement;
    const options = within(clientSelect)
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(options).toEqual(["Use default client", "qBittorrent", "Transmission"]);
  });

  it("sends client_id, download_dir and category in the create payload", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));
    mockApi.post.mockResolvedValue({});

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    const clientSelect = await screen.findByLabelText(/^client \(optional\)$/i);
    await user.selectOptions(clientSelect, "c1");
    await user.type(screen.getByLabelText(/download folder/i), "/downloads/tv");
    await user.type(screen.getByLabelText(/^category \(optional\)$/i), "tv");

    await user.click(screen.getByRole("button", { name: /add topic/i }));

    expect(mockApi.post).toHaveBeenCalledWith(
      "/topics",
      expect.objectContaining({
        url: LOSTFILM_URL,
        client_id: "c1",
        download_dir: "/downloads/tv",
        category: "tv",
      }),
    );
  });
});

// Match advertising a quality list so the edit form renders the quality
// <select> and we can assert its prefilled value.
const qualityCatalogMatch = {
  ...catalogMatch,
  qualities: ["720p", "1080p", "2160p"],
};

function routeGetWithQuality(seasonsImpl: () => unknown) {
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match")) return Promise.resolve(qualityCatalogMatch);
    if (path.startsWith("/trackers/seasons")) return seasonsImpl();
    if (path.startsWith("/clients")) return Promise.resolve(clientsList);
    return Promise.resolve({ credentials: [] });
  });
}

describe("EditTopicCard — prefill + update", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("prefills the form from the topic's current values", async () => {
    routeGetWithQuality(() =>
      Promise.resolve({ seasons: [{ number: 1, episodes: [1, 2] }, { number: 2, episodes: [1, 2, 3] }] }),
    );

    renderEdit();

    // URL is read-only.
    const urlInput = screen.getByLabelText(/url or magnet link/i) as HTMLInputElement;
    expect(urlInput.value).toBe(LOSTFILM_URL);
    expect(urlInput).toBeDisabled();

    expect((screen.getByLabelText(/display name/i) as HTMLInputElement).value).toBe(
      "Some Show",
    );

    // Quality select prefilled once the match (with qualities) resolves.
    const qualitySelect = (await screen.findByLabelText(/^quality$/i)) as HTMLSelectElement;
    expect(qualitySelect.value).toBe("1080p");

    expect((screen.getByLabelText(/download folder/i) as HTMLInputElement).value).toBe(
      "/downloads/shows",
    );
    expect(
      (screen.getByLabelText(/^category \(optional\)$/i) as HTMLInputElement).value,
    ).toBe("series");

    // Client select prefilled to the topic's client.
    const clientSelect = (await screen.findByLabelText(
      /^client \(optional\)$/i,
    )) as HTMLSelectElement;
    expect(clientSelect.value).toBe("c2");
  });

  it("renders the season-catalog dropdowns prefilled from the topic", async () => {
    routeGetWithQuality(() =>
      Promise.resolve({ seasons: [{ number: 1, episodes: [1, 2] }, { number: 2, episodes: [1, 2, 3] }] }),
    );

    renderEdit();

    // Wait for the catalog-specific option to appear — the "Start from
    // season" label is shared with the free-text fallback, so we key off
    // the dropdown's option to know the catalog (SELECT) rendered.
    await screen.findByRole("option", { name: "Season 2" });

    // Catalog dropdown renders (SELECT, not the free-text fallback) and is
    // prefilled to the topic's start_season / start_episode.
    const seasonSelect = screen.getByLabelText(/start from season/i) as HTMLSelectElement;
    expect(seasonSelect.tagName).toBe("SELECT");
    expect(seasonSelect.value).toBe("2");

    const episodeSelect = screen.getByLabelText(/start from episode/i) as HTMLSelectElement;
    expect(episodeSelect.value).toBe("3");
  });

  it("calls updateTopic with the topic id and edited fields", async () => {
    const user = userEvent.setup();
    routeGetWithQuality(() => Promise.resolve({ seasons: [] }));
    mockApi.updateTopic.mockResolvedValue({});

    const { onSaved } = renderEdit();

    const displayInput = await screen.findByLabelText(/display name/i);
    await user.clear(displayInput);
    await user.type(displayInput, "Renamed Show");

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockApi.updateTopic).toHaveBeenCalledWith(
      "t-1",
      expect.objectContaining({
        display_name: "Renamed Show",
        client_id: "c2",
        download_dir: "/downloads/shows",
        category: "series",
        quality: "1080p",
      }),
    );
    expect(onSaved).toHaveBeenCalled();
  });
});
