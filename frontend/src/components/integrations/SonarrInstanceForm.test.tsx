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
      createSonarrInstance: vi.fn(),
      updateSonarrInstance: vi.fn(),
      testSonarr: vi.fn(),
      testSonarrInstance: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";
import { SonarrInstanceForm } from "./SonarrInstanceForm";

const mockApi = api as unknown as {
  createSonarrInstance: ReturnType<typeof vi.fn>;
  updateSonarrInstance: ReturnType<typeof vi.fn>;
  testSonarr: ReturnType<typeof vi.fn>;
  testSonarrInstance: ReturnType<typeof vi.fn>;
};

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe("SonarrInstanceForm", () => {
  beforeEach(() => vi.clearAllMocks());

  it("creates a new instance with the typed name and an empty api_key", async () => {
    mockApi.createSonarrInstance.mockResolvedValue({});
    const onDone = vi.fn();
    wrap(
      <SonarrInstanceForm initial={null} clients={[]} trackers={[]} onDone={onDone} onCancel={() => {}} />,
    );

    await userEvent.type(screen.getByPlaceholderText(/TV, Anime/i), "Anime");
    await userEvent.type(screen.getByPlaceholderText("http://sonarr:8989"), "http://anime:8989");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(mockApi.createSonarrInstance).toHaveBeenCalled());
    const body = mockApi.createSonarrInstance.mock.calls[0][0];
    expect(body.name).toBe("Anime");
    expect(body.api_key).toBe("");
    await waitFor(() => expect(onDone).toHaveBeenCalled());
  });

  it("tests an existing instance's stored key when the key field is blank", async () => {
    mockApi.testSonarrInstance.mockResolvedValue({ ok: true, version: "4.1.0", app_name: "Sonarr" });
    const existing = {
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
    wrap(
      <SonarrInstanceForm initial={existing} clients={[]} trackers={[]} onDone={() => {}} onCancel={() => {}} />,
    );

    await userEvent.click(screen.getByRole("button", { name: /test connection/i }));

    await waitFor(() => expect(mockApi.testSonarrInstance).toHaveBeenCalledWith("i1"));
    expect(mockApi.testSonarr).not.toHaveBeenCalled();
    expect(await screen.findByText(/4\.1\.0/)).toBeInTheDocument();
  });
});
