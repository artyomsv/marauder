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
  errors: [{ tracker_name: "rutracker", error: "search requires credentials" }],
};

beforeEach(() => {
  mockApi.get.mockReset();
});

describe("TrackerSearch", () => {
  it("does not search while typing; fires once on submit", async () => {
    mockApi.get.mockResolvedValue(RESULTS);
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    expect(mockApi.get).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: /search/i }));
    expect(await screen.findByText("Test release 1080p")).toBeInTheDocument();
    expect(mockApi.get).toHaveBeenCalledTimes(1);
    expect(mockApi.get).toHaveBeenCalledWith("/trackers/search?q=test");
  });

  it("renders result metadata and reports selection", async () => {
    mockApi.get.mockResolvedValue(RESULTS);
    const onSelect = vi.fn();
    render(<TrackerSearch onSelect={onSelect} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    const row = await screen.findByText("Test release 1080p");
    expect(screen.getByText("Rutor.org")).toBeInTheDocument();
    expect(screen.getByText("1.4 GB")).toBeInTheDocument();
    expect(screen.getByText("↑17")).toBeInTheDocument();

    await userEvent.click(row);
    expect(onSelect).toHaveBeenCalledWith("https://rutor.org/torrent/975045");
  });

  it("maps the credentials error to a friendly notice", async () => {
    mockApi.get.mockResolvedValue(RESULTS);
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "test");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText(/needs a tracker account/i)).toBeInTheDocument();
  });

  it("shows an honest empty state after a search with no results", async () => {
    mockApi.get.mockResolvedValue({ results: [], errors: [] });
    render(<TrackerSearch onSelect={() => {}} />, { wrapper: wrap() });

    await userEvent.type(screen.getByRole("textbox"), "nothing");
    await userEvent.click(screen.getByRole("button", { name: /search/i }));

    expect(await screen.findByText("No results")).toBeInTheDocument();
  });
});
