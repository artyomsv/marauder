import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      updateSonarrInstance: vi.fn(),
      deleteSonarrInstance: vi.fn(),
      testSonarrInstance: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";
import { SonarrInstanceCard } from "./SonarrInstanceCard";

const mockApi = api as unknown as {
  updateSonarrInstance: ReturnType<typeof vi.fn>;
  deleteSonarrInstance: ReturnType<typeof vi.fn>;
  testSonarrInstance: ReturnType<typeof vi.fn>;
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

describe("SonarrInstanceCard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("toggles enabled via update when Disable is clicked", async () => {
    mockApi.updateSonarrInstance.mockResolvedValue({ ...inst, enabled: false });
    wrap(<SonarrInstanceCard instance={inst} clients={[]} trackers={[]} />);

    await userEvent.click(screen.getByRole("button", { name: /^disable$/i }));

    await waitFor(() => expect(mockApi.updateSonarrInstance).toHaveBeenCalled());
    const [id, body] = mockApi.updateSonarrInstance.mock.calls[0];
    expect(id).toBe("i1");
    expect(body.enabled).toBe(false);
    expect(body.api_key).toBe(""); // stored key preserved
  });

  it("deletes after the inline confirm", async () => {
    mockApi.deleteSonarrInstance.mockResolvedValue(undefined);
    wrap(<SonarrInstanceCard instance={inst} clients={[]} trackers={[]} />);

    await userEvent.click(screen.getByRole("button", { name: /delete instance/i }));
    await userEvent.click(screen.getByRole("button", { name: /confirm delete instance/i }));

    await waitFor(() => expect(mockApi.deleteSonarrInstance).toHaveBeenCalledWith("i1"));
  });

  it("surfaces the Sonarr version on a successful test", async () => {
    mockApi.testSonarrInstance.mockResolvedValue({ ok: true, version: "4.0.14", app_name: "Sonarr" });
    wrap(<SonarrInstanceCard instance={inst} clients={[]} trackers={[]} />);

    await userEvent.click(screen.getByRole("button", { name: /test connection/i }));

    expect(await screen.findByText(/4\.0\.14/)).toBeInTheDocument();
  });
});
