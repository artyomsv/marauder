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
      getClientCategories: vi.fn(),
      getSettings: vi.fn(),
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
  getClientCategories: ReturnType<typeof vi.fn>;
  getSettings: ReturnType<typeof vi.fn>;
};

const editableInstance = {
  id: "i1",
  name: "TV",
  enabled: true,
  sonarr_url: "http://sonarr:8989",
  api_key_set: true,
  poll_interval_sec: 900,
  allowed_trackers: [] as string[],
  default_client_id: "c1",
  default_category: "",
  default_download_dir: "",
  default_replace_on_update: null,
  default_replace_delete_data: null,
  update_existing: false,
  last_seen_at: null,
  created_at: "2026-06-27T00:00:00Z",
  updated_at: "2026-06-27T00:00:00Z",
};

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe("SonarrInstanceForm", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: false, default_replace_on_update: true, default_replace_delete_data: false });
  });

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

  it("offers the selected client's categories in the category combobox", async () => {
    mockApi.getClientCategories.mockResolvedValue({
      supported: true,
      categories: ["tv-sonarr", "sonarr-e2e"],
    });
    const clients = [{ id: "c1", display_name: "qBittorrent" }];
    wrap(
      <SonarrInstanceForm
        initial={editableInstance}
        clients={clients}
        trackers={[]}
        onDone={() => {}}
        onCancel={() => {}}
      />,
    );

    // The combobox renders the fetched categories as options once loaded.
    expect(await screen.findByRole("option", { name: "tv-sonarr" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "sonarr-e2e" })).toBeInTheDocument();
    expect(mockApi.getClientCategories).toHaveBeenCalledWith("c1");
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
      default_replace_on_update: null,
      default_replace_delete_data: null,
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

describe("Sonarr replace defaults", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: false, default_replace_on_update: true, default_replace_delete_data: false });
    mockApi.updateSonarrInstance.mockResolvedValue({});
  });

  it("shows global values and saves independent overrides or inheritance", async () => {
    wrap(<SonarrInstanceForm initial={editableInstance} clients={[]} trackers={[]} onDone={() => {}} onCancel={() => {}} />);
    const replace = screen.getByRole("combobox", { name: /replace previous version by default/i });
    const deleteData = screen.getByRole("combobox", { name: /delete the old files by default/i });
    expect(replace).toHaveValue("inherit");
    expect(deleteData).toHaveValue("inherit");
    expect(await screen.findByRole("option", { name: "Use global default (currently: On)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Use global default (currently: Off)" })).toBeInTheDocument();
    await userEvent.selectOptions(replace, "true");
    await userEvent.selectOptions(deleteData, "false");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(mockApi.updateSonarrInstance).toHaveBeenCalledWith("i1", expect.objectContaining({ default_replace_on_update: true, default_replace_delete_data: false })));
    await userEvent.selectOptions(replace, "inherit");
    await userEvent.selectOptions(deleteData, "inherit");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(mockApi.updateSonarrInstance).toHaveBeenLastCalledWith("i1", expect.objectContaining({ default_replace_on_update: null, default_replace_delete_data: null })));
  });

  it.each([false, true])("gates delete-data on the effective replace value (global=%s)", async (globalReplace) => {
    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: false, default_replace_on_update: globalReplace, default_replace_delete_data: true });
    wrap(<SonarrInstanceForm initial={null} clients={[]} trackers={[]} onDone={() => {}} onCancel={() => {}} />);
    const replace = screen.getByRole("combobox", { name: /replace previous version by default/i });
    const deleteData = screen.getByRole("combobox", { name: /delete the old files by default/i });
    await waitFor(() => expect((deleteData as HTMLSelectElement).disabled).toBe(!globalReplace));
    await userEvent.selectOptions(replace, "true");
    expect(deleteData).not.toBeDisabled();
    await userEvent.selectOptions(replace, "false");
    expect(deleteData).toBeDisabled();
    await userEvent.selectOptions(replace, "inherit");
    expect((deleteData as HTMLSelectElement).disabled).toBe(!globalReplace);
  });

  it("does not claim to know the global value while settings are unavailable", () => {
    mockApi.getSettings.mockReturnValue(new Promise(() => {}));
    wrap(<SonarrInstanceForm initial={null} clients={[]} trackers={[]} onDone={() => {}} onCancel={() => {}} />);
    expect(screen.getAllByRole("option", { name: "Use global default" })).toHaveLength(2);
    expect(screen.queryByRole("option", { name: /currently/ })).not.toBeInTheDocument();
  });
});
