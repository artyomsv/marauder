import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

// The card talks to the backend through the `api` object and reads the
// tracker list via useSystemInfo — mock both to exercise rendered behaviour
// without a network.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      getSonarrConfig: vi.fn(),
      updateSonarrConfig: vi.fn(),
      testSonarr: vi.fn(),
    },
  };
});

vi.mock("@/lib/hooks/useSystemInfo", () => ({
  useSystemInfo: () => ({
    data: {
      trackers: [
        { name: "rutracker", display_name: "RuTracker", supports_interactive_login: false, supports_credentials: true },
        { name: "kinozal", display_name: "Kinozal", supports_interactive_login: false, supports_credentials: false },
      ],
    },
  }),
}));

import { api } from "@/lib/api";
import { SonarrCard } from "./SonarrCard";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  getSonarrConfig: ReturnType<typeof vi.fn>;
  updateSonarrConfig: ReturnType<typeof vi.fn>;
  testSonarr: ReturnType<typeof vi.fn>;
};

const baseConfig = {
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
};

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe("SonarrCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.get.mockResolvedValue({ clients: [{ id: "c1", display_name: "qBittorrent" }] });
    mockApi.getSonarrConfig.mockResolvedValue({ ...baseConfig });
  });

  it("loads the config and lists supported trackers", async () => {
    wrap(<SonarrCard />);
    expect(await screen.findByDisplayValue("http://sonarr:8989")).toBeInTheDocument();
    expect(screen.getByText("RuTracker")).toBeInTheDocument();
    expect(screen.getByText("Kinozal")).toBeInTheDocument();
  });

  it("saves with an empty api_key so the stored key is preserved", async () => {
    mockApi.updateSonarrConfig.mockResolvedValue({ ...baseConfig });
    wrap(<SonarrCard />);
    await screen.findByDisplayValue("http://sonarr:8989");

    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(mockApi.updateSonarrConfig).toHaveBeenCalled());
    const body = mockApi.updateSonarrConfig.mock.calls[0][0];
    expect(body.api_key).toBe("");
    expect(body.sonarr_url).toBe("http://sonarr:8989");
  });

  it("surfaces the Sonarr version on a successful test", async () => {
    mockApi.testSonarr.mockResolvedValue({ ok: true, version: "4.0.14", app_name: "Sonarr" });
    wrap(<SonarrCard />);
    await screen.findByDisplayValue("http://sonarr:8989");

    await userEvent.click(screen.getByRole("button", { name: /test connection/i }));

    expect(await screen.findByText(/4\.0\.14/)).toBeInTheDocument();
  });
});
