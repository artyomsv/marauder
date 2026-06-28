import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      listSonarrInstances: vi.fn(),
    },
  };
});

vi.mock("@/lib/hooks/useSystemInfo", () => ({
  useSystemInfo: () => ({ data: { trackers: [] } }),
}));

import { api } from "@/lib/api";
import { SonarrInstanceList } from "./SonarrInstanceList";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  listSonarrInstances: ReturnType<typeof vi.fn>;
};

const inst = {
  id: "i1",
  name: "TV",
  enabled: true,
  sonarr_url: "http://sonarr:8989",
  api_key_set: true,
  poll_interval_sec: 900,
  allowed_trackers: [] as string[],
  default_client_id: null,
  default_category: "tv-sonarr",
  default_download_dir: "",
  update_existing: false,
  last_seen_at: null,
  created_at: "2026-06-27T00:00:00Z",
  updated_at: "2026-06-27T00:00:00Z",
};

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe("SonarrInstanceList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.get.mockResolvedValue({ clients: [{ id: "c1", display_name: "qBittorrent" }] });
  });

  it("shows the empty state when there are no instances", async () => {
    mockApi.listSonarrInstances.mockResolvedValue([]);
    wrap(<SonarrInstanceList />);
    expect(await screen.findByText(/no sonarr instances yet/i)).toBeInTheDocument();
  });

  it("renders a card per instance with its status badge", async () => {
    mockApi.listSonarrInstances.mockResolvedValue([inst]);
    wrap(<SonarrInstanceList />);
    expect(await screen.findByText("TV")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
  });
});
