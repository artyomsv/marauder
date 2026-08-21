import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

// The component talks to the backend only through api.get.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { get: vi.fn() } };
});

import { api } from "@/lib/api";
import { TrackerSearch } from "./TrackerSearch";

const mockApi = api as unknown as { get: ReturnType<typeof vi.fn> };

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const RESULTS = {
  results: [
    {
      tracker_name: "rutor",
      tracker_display_name: "Rutor.org",
      title: "Test release 1080p",
      url: "https://rutor.org/torrent/975045",
      size: "1.4 GB",
      seeders: 17,
    },
  ],
  errors: [
    {
      tracker_name: "rutracker",
      tracker_display_name: "RuTracker.org",
      code: "no_credentials",
      error: "search requires credentials",
    },
  ],
};

const SYSTEM_INFO = {
  version: { version: "test", commit: "", buildDate: "" },
  trackers: [
    {
      name: "rutor",
      display_name: "Rutor.org",
      supports_interactive_login: false,
      supports_credentials: false,
      supports_search: true,
    },
    {
      name: "rutracker",
      display_name: "RuTracker.org",
      supports_interactive_login: false,
      supports_credentials: true,
      supports_search: true,
    },
    {
      name: "toloka",
      display_name: "Toloka",
      supports_interactive_login: false,
      supports_credentials: true,
      supports_search: false,
    },
  ],
  clients: [],
  notifiers: [],
};

// Dispatches by path: the component queries /system/info (coverage widget)
// and /trackers/search. searchCalls() counts only real searches so
// call-count assertions ignore the manifest fetch.
function mockSearchResponse(response: unknown) {
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/system/info")) return Promise.resolve(SYSTEM_INFO);
    return Promise.resolve(response);
  });
}

const searchCalls = () =>
  mockApi.get.mock.calls.filter(([p]) => String(p).startsWith("/trackers/search"));

beforeEach(() => {
  mockApi.get.mockReset();
});

describe("TrackerSearch", () => {
  it("does not search while typing; fires once on submit", async () => {
    mockSearchResponse(RESULTS);
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    expect(searchCalls()).toHaveLength(0);

    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    expect(await screen.findByText("Test release 1080p")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(1);
    expect(mockApi.get).toHaveBeenCalledWith("/trackers/search?q=test");
  });

  it("lists the searchable trackers from the plugin manifest", async () => {
    mockSearchResponse(RESULTS);
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    expect(await screen.findByText("Searches:")).toBeInTheDocument();
    expect(screen.getByText("Rutor.org")).toBeInTheDocument();
    expect(screen.getByText("RuTracker.org")).toBeInTheDocument();
    // Non-searchable trackers must not appear in the coverage widget.
    expect(screen.queryByText("Toloka")).toBeNull();
  });

  it("renders result metadata and reports selection", async () => {
    mockSearchResponse(RESULTS);
    const onSelect = vi.fn();
    render(<TrackerSearch onSelect={onSelect} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    const row = await screen.findByText("Test release 1080p");
    // "Rutor.org" appears in both the coverage widget and the result badge.
    expect(screen.getAllByText("Rutor.org").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("1.4 GB")).toBeInTheDocument();
    expect(screen.getByText("↑17")).toBeInTheDocument();

    await userEvent.click(row);
    expect(onSelect).toHaveBeenCalledWith("https://rutor.org/torrent/975045");
  });

  it("maps the credentials error to a friendly notice", async () => {
    mockSearchResponse(RESULTS);
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText(/needs a tracker account/i)).toBeInTheDocument();
  });

  it("shows an honest empty state after a search with no results", async () => {
    mockSearchResponse({ results: [], errors: [] });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "nothing");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText("No results")).toBeInTheDocument();
  });

  it("shows the failure notice and actually retries the same query", async () => {
    let searchAttempts = 0;
    mockApi.get.mockImplementation((path: string) => {
      if (path.startsWith("/system/info")) return Promise.resolve(SYSTEM_INFO);
      searchAttempts += 1;
      return searchAttempts === 1
        ? Promise.reject(new Error("boom"))
        : Promise.resolve(RESULTS);
    });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    expect(await screen.findByText(/search failed\. try again\./i)).toBeInTheDocument();

    // Same query, second click — must refetch, not silently no-op.
    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    expect(await screen.findByText("Test release 1080p")).toBeInTheDocument();
    expect(searchCalls()).toHaveLength(2);
  });

  it("labels login_failed differently from a missing account", async () => {
    mockSearchResponse({
      results: [],
      errors: [
        {
          tracker_name: "rutracker",
          tracker_display_name: "RuTracker.org",
          code: "login_failed",
          error: "tracker login failed",
        },
      ],
    });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText(/tracker login failed/i)).toBeInTheDocument();
    expect(screen.queryByText(/needs a tracker account/i)).toBeNull();
  });

  // Issue #158 on the search surface. RuTracker's search is login-gated, so a
  // missing Cloudflare solver arrives as a failed login and was rendered
  // "check your account under Accounts" — sending the user to fix a credential
  // that is fine. The two solver states must be distinguishable from a real
  // auth failure and from each other, since one is "you have no solver" and
  // the other is "yours is down".
  it("blames the missing solver, not the account", async () => {
    mockSearchResponse({
      results: [],
      errors: [
        {
          tracker_name: "rutracker",
          tracker_display_name: "RuTracker.org",
          code: "solver_missing",
          error: "no Cloudflare solver is configured",
        },
      ],
    });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText(/no Cloudflare solver/i)).toBeInTheDocument();
    expect(screen.queryByText(/check your account/i)).toBeNull();
    expect(screen.queryByText(/needs a tracker account/i)).toBeNull();
  });

  it("distinguishes a solver that is down from one that is absent", async () => {
    mockSearchResponse({
      results: [],
      errors: [
        {
          tracker_name: "rutracker",
          tracker_display_name: "RuTracker.org",
          code: "solver",
          error: "the Cloudflare solver did not answer",
        },
      ],
    });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText(/solver isn't responding/i)).toBeInTheDocument();
    expect(screen.queryByText(/check your account/i)).toBeNull();
  });
});
