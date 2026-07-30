import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

import { AddTopicCard, TopicsPage } from "./Topics";
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
      previewTracker: vi.fn(),
      resetTopic: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  post: ReturnType<typeof vi.fn>;
  updateTopic: ReturnType<typeof vi.fn>;
  previewTracker: ReturnType<typeof vi.fn>;
  resetTopic: ReturnType<typeof vi.fn>;
};

const LOSTFILM_URL = "https://www.lostfilm.tv/series/Some_Show/seasons";

const catalogMatch = {
  tracker_name: "lostfilm",
  display_name: "LostFilm",
  supports_episode_filter: true,
  supports_season_catalog: true,
  requires_credentials: false,
  credentials_optional: false,
  uses_cloudflare: false,
};

// Two clients the picker should list, plus the "Use default client" option.
const clientsList = {
  clients: [
    { id: "c1", display_name: "qBittorrent" },
    { id: "c2", display_name: "Transmission" },
  ],
};

// Two notifiers the picker should list, plus the "Use default notifiers" option.
const notifiersList = {
  notifiers: [
    { id: "n1", display_name: "Telegram" },
    { id: "n2", display_name: "Email" },
  ],
};

// Routes /trackers/match, /trackers/seasons, /clients and /notifiers;
// everything else (e.g. the /credentials list the card also reads) resolves empty.
function routeGet(seasonsImpl: () => unknown) {
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match")) return Promise.resolve(catalogMatch);
    if (path.startsWith("/trackers/seasons")) return seasonsImpl();
    if (path.startsWith("/clients")) return Promise.resolve(clientsList);
    if (path.startsWith("/notifiers")) return Promise.resolve(notifiersList);
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
  ImageURL: "",
  ClientID: "c2",
  NotifierID: null,
  DownloadDir: "/downloads/shows",
  Category: "series",
  ReplaceOnUpdate: false,
  ReplaceDeleteData: true,
  Extra: { quality: "1080p", start_season: 2, start_episode: 3 },
  LastHash: "",
  LastCheckedAt: null,
  LastUpdatedAt: null,
  NextCheckAt: "",
  CheckIntervalSec: 900,
  ConsecutiveErrors: 0,
  Status: "active",
  LastError: "",
  LastErrorCode: "",
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

describe("AddTopicCard — credentials hint", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const NNM_URL = "https://nnmclub.to/forum/viewtopic.php?t=420880";

  function mockMatch(opts: { requires: boolean; optional: boolean }) {
    mockApi.get.mockImplementation((path: string) => {
      if (path.startsWith("/trackers/match")) {
        return Promise.resolve({
          tracker_name: "nnmclub",
          display_name: "NNM-Club.to",
          supports_episode_filter: false,
          supports_season_catalog: false,
          requires_credentials: opts.requires,
          credentials_optional: opts.optional,
          uses_cloudflare: true,
        });
      }
      if (path.startsWith("/clients")) return Promise.resolve(clientsList);
      return Promise.resolve({ credentials: [] });
    });
    mockApi.previewTracker.mockResolvedValue({ title: "", image_url: "" });
  }

  it("shows an optional (not required) hint when credentials are optional", async () => {
    const user = userEvent.setup();
    mockMatch({ requires: false, optional: true });

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), NNM_URL);

    expect(
      await screen.findByText(/works without an account/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/requires login credentials/i),
    ).not.toBeInTheDocument();
  });

  it("shows the required hint when credentials are required", async () => {
    const user = userEvent.setup();
    mockMatch({ requires: true, optional: false });

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), NNM_URL);

    expect(
      await screen.findByText(/requires login credentials/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/works without an account/i),
    ).not.toBeInTheDocument();
  });
});

describe("AddTopicCard — display name auto-fill on URL change", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // Routes /trackers/match and /trackers/preview to tracker-specific values
  // keyed off the URL, so switching the URL changes both responses.
  function routeMatchAndPreviewByUrl() {
    mockApi.get.mockImplementation((path: string) => {
      if (path.startsWith("/trackers/match")) {
        const tracker = path.includes("kinozal") ? "kinozal" : "rutracker";
        return Promise.resolve({
          tracker_name: tracker,
          display_name: tracker,
          supports_episode_filter: false,
          supports_season_catalog: false,
          requires_credentials: false,
          credentials_optional: false,
          uses_cloudflare: false,
        });
      }
      if (path.startsWith("/clients")) return Promise.resolve(clientsList);
      return Promise.resolve({ credentials: [] });
    });
    mockApi.previewTracker.mockImplementation((url: string) =>
      Promise.resolve(
        url.includes("kinozal")
          ? { title: "Kinozal Show", image_url: "" }
          : { title: "RuTracker Show", image_url: "" },
      ),
    );
  }

  const RUTRACKER_URL = "https://rutracker.org/forum/viewtopic.php?t=1";
  const KINOZAL_URL = "https://kinozal.tv/details.php?id=2136770";

  it("replaces an auto-filled name when the URL changes to a different tracker", async () => {
    const user = userEvent.setup();
    routeMatchAndPreviewByUrl();

    renderCard();
    const urlInput = screen.getByLabelText(/url or magnet link/i);
    await user.type(urlInput, RUTRACKER_URL);

    const displayInput = (await screen.findByLabelText(
      /display name/i,
    )) as HTMLInputElement;
    await waitFor(() => expect(displayInput.value).toBe("RuTracker Show"));

    await user.clear(urlInput);
    await user.type(urlInput, KINOZAL_URL);

    await waitFor(() => expect(displayInput.value).toBe("Kinozal Show"));
  });

  it("keeps a user-typed name even when the URL changes", async () => {
    const user = userEvent.setup();
    routeMatchAndPreviewByUrl();

    renderCard();
    const urlInput = screen.getByLabelText(/url or magnet link/i);
    await user.type(urlInput, RUTRACKER_URL);

    const displayInput = (await screen.findByLabelText(
      /display name/i,
    )) as HTMLInputElement;
    await user.clear(displayInput);
    await user.type(displayInput, "My Custom Name");

    await user.clear(urlInput);
    await user.type(urlInput, KINOZAL_URL);

    // User intent wins: the auto-fill must not clobber a typed name.
    await waitFor(() =>
      expect(mockApi.previewTracker).toHaveBeenCalledWith(KINOZAL_URL),
    );
    expect(displayInput.value).toBe("My Custom Name");
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

  it("defaults replace-on-update off and hides the delete-data sub-option", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));
    mockApi.post.mockResolvedValue({});

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);
    await screen.findByLabelText(/^client \(optional\)$/i);

    // The delete-data sub-option only renders once replace is enabled.
    expect(screen.queryByLabelText(/delete the old files/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /add topic/i }));
    expect(mockApi.post).toHaveBeenCalledWith(
      "/topics",
      expect.objectContaining({ replace_on_update: false, replace_delete_data: true }),
    );
  });

  it("sends replace_on_update with delete-data toggled off in the create payload", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));
    mockApi.post.mockResolvedValue({});

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);
    await screen.findByLabelText(/^client \(optional\)$/i);

    await user.click(screen.getByLabelText(/replace previous version on update/i));
    // Sub-option now visible; turn off the default delete-data behaviour.
    await user.click(await screen.findByLabelText(/delete the old files/i));

    await user.click(screen.getByRole("button", { name: /add topic/i }));
    expect(mockApi.post).toHaveBeenCalledWith(
      "/topics",
      expect.objectContaining({ replace_on_update: true, replace_delete_data: false }),
    );
  });

  it("lists the user's notifiers plus a default option", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    // Wait for the client picker to confirm the form has fully loaded.
    await screen.findByLabelText(/^client \(optional\)$/i);

    const notifierSelect = screen.getByLabelText(
      /^notifier \(optional\)$/i,
    ) as HTMLSelectElement;
    const options = within(notifierSelect)
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(options).toEqual(["Use default notifiers", "Telegram", "Email"]);
  });

  it("sends notifier_id in the create payload when a notifier is selected", async () => {
    const user = userEvent.setup();
    routeGet(() => Promise.resolve({ seasons: [] }));
    mockApi.post.mockResolvedValue({});

    renderCard();
    await user.type(screen.getByLabelText(/url or magnet link/i), LOSTFILM_URL);

    // Wait for the notifier select to be available.
    await screen.findByLabelText(/^client \(optional\)$/i);
    const notifierSelect = screen.getByLabelText(/^notifier \(optional\)$/i);
    await user.selectOptions(notifierSelect, "n1");

    await user.click(screen.getByRole("button", { name: /add topic/i }));

    expect(mockApi.post).toHaveBeenCalledWith(
      "/topics",
      expect.objectContaining({
        url: LOSTFILM_URL,
        notifier_id: "n1",
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
    if (path.startsWith("/notifiers")) return Promise.resolve(notifiersList);
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

  it("pre-fills the replace toggle from the topic and sends it on save", async () => {
    const user = userEvent.setup();
    routeGetWithQuality(() => Promise.resolve({ seasons: [] }));
    mockApi.updateTopic.mockResolvedValue({});

    renderEdit({ ...EXISTING_TOPIC, ReplaceOnUpdate: true, ReplaceDeleteData: false });

    const replaceToggle = (await screen.findByLabelText(
      /replace previous version on update/i,
    )) as HTMLInputElement;
    expect(replaceToggle.checked).toBe(true);
    // Sub-option is visible (replace is on) and pre-filled to keep-data.
    const deleteToggle = screen.getByLabelText(/delete the old files/i) as HTMLInputElement;
    expect(deleteToggle.checked).toBe(false);

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockApi.updateTopic).toHaveBeenCalledWith(
      "t-1",
      expect.objectContaining({ replace_on_update: true, replace_delete_data: false }),
    );
  });

  it("sends download_dir: \"\" when the folder is emptied (explicit clear)", async () => {
    const user = userEvent.setup();
    routeGetWithQuality(() => Promise.resolve({ seasons: [] }));
    mockApi.updateTopic.mockResolvedValue({});

    renderEdit();

    const folderInput = await screen.findByLabelText(/download folder/i);
    await user.clear(folderInput);

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockApi.updateTopic).toHaveBeenCalledWith(
      "t-1",
      expect.objectContaining({ download_dir: "" }),
    );
  });

  it("sends notifier_id when a notifier is selected on edit", async () => {
    const user = userEvent.setup();
    routeGetWithQuality(() => Promise.resolve({ seasons: [] }));
    mockApi.updateTopic.mockResolvedValue({});

    renderEdit();

    // Wait for the form to fully load (quality select implies match resolved).
    await screen.findByLabelText(/^quality$/i);

    const notifierSelect = screen.getByLabelText(/^notifier \(optional\)$/i);
    await user.selectOptions(notifierSelect, "n1");

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockApi.updateTopic).toHaveBeenCalledWith(
      "t-1",
      expect.objectContaining({ notifier_id: "n1" }),
    );
  });

  it("pre-fills notifier_id from topic.NotifierID and sends it on save", async () => {
    const user = userEvent.setup();
    routeGetWithQuality(() => Promise.resolve({ seasons: [] }));
    mockApi.updateTopic.mockResolvedValue({});

    // Render a topic that already has NotifierID set to "n2".
    renderEdit({ ...EXISTING_TOPIC, NotifierID: "n2" });

    // Wait for the notifier options to load before asserting the prefilled value.
    await screen.findByRole("option", { name: "Email" });
    const notifierSelect = screen.getByLabelText(
      /^notifier \(optional\)$/i,
    ) as HTMLSelectElement;
    expect(notifierSelect.value).toBe("n2");

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(mockApi.updateTopic).toHaveBeenCalledWith(
      "t-1",
      expect.objectContaining({ notifier_id: "n2" }),
    );
  });
});

const RESET_ROW_TOPICS: Topic[] = [
  { ...EXISTING_TOPIC, ID: "t1", DisplayName: "Alpha", TrackerName: "rutracker", URL: "https://x/1" },
  { ...EXISTING_TOPIC, ID: "t2", DisplayName: "Beta", TrackerName: "rutracker", URL: "https://x/2" },
];

function renderTopicsPage() {
  return render(<TopicsPage />, { wrapper: wrapperWithClient() });
}

describe("TopicsPage reset", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.get.mockImplementation((path: string) => {
      if (path === "/topics") return Promise.resolve({ topics: RESET_ROW_TOPICS });
      if (path === "/clients") return Promise.resolve({ clients: [] });
      if (path === "/notifiers") return Promise.resolve({ notifiers: [] });
      return Promise.resolve({});
    });
  });

  it("opens the reset card for the topic whose row was clicked", async () => {
    renderTopicsPage();
    await screen.findByText("Alpha");

    // Two fixture topics, two reset buttons. Exercise both indices so this
    // proves each row's action closes over its own topic — clicking index 0
    // and only ever checking "Reset Alpha" would pass identically if every
    // row's button were (incorrectly) bound to topics[0].
    const resetButtons = screen.getAllByRole("button", { name: /reset topic/i });

    await userEvent.click(resetButtons[0]);
    expect(await screen.findByText(/Reset Alpha/)).toBeInTheDocument();

    await userEvent.click(resetButtons[1]);
    expect(await screen.findByText(/Reset Beta/)).toBeInTheDocument();
  });

  it("resets every selected topic from the bulk bar", async () => {
    mockApi.resetTopic.mockResolvedValue({ removed: 0, warnings: [] });
    renderTopicsPage();
    await screen.findByText("Alpha");

    await userEvent.click(screen.getByLabelText("Select all"));
    await userEvent.click(screen.getByRole("button", { name: /^reset$/i }));
    // The bulk bar's own "Reset" button (outline, opens the card) shares an
    // accessible name with the card's "Reset" confirm button (destructive,
    // submits). Scope to the card — identified by its title row's closest
    // Card ancestor — so this queries the confirm button, not the bar's.
    const card = (await screen.findByText(/Reset 2 topics/)).closest("div")!.parentElement!;
    await userEvent.click(within(card).getByRole("button", { name: /^reset$/i }));

    await waitFor(() => expect(mockApi.resetTopic).toHaveBeenCalledTimes(2));
    expect(mockApi.resetTopic.mock.calls.map((c: unknown[]) => c[0]).sort()).toEqual(["t1", "t2"]);
  });
});
