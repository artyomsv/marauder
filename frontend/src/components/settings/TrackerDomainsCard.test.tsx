import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn().mockResolvedValue({
        version: { version: "1.0.0", commit: "abc", buildDate: "2026-01-01" },
        trackers: [],
      }),
      post: vi.fn(),
      listTrackerDomains: vi.fn(),
      updateTrackerDomains: vi.fn(),
      testTrackerDomain: vi.fn(),
    },
  };
});

// Drives the admin-gate for the SettingsPage test.
let role: "admin" | "user" = "admin";
vi.mock("@/lib/auth-store", () => ({
  useAuthStore: (
    sel: (s: {
      user: { id: string; username: string; email: string; role: string };
      refreshToken: string | null;
      logout: () => void;
    }) => unknown,
  ) => sel({ user: { id: "u1", username: "admin", email: "", role }, refreshToken: null, logout: () => {} }),
}));

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { TrackerDomainsCard } from "./TrackerDomainsCard";
import { SettingsPage } from "@/pages/Settings";

const mockApi = api as unknown as {
  listTrackerDomains: ReturnType<typeof vi.fn>;
  updateTrackerDomains: ReturnType<typeof vi.fn>;
  testTrackerDomain: ReturnType<typeof vi.fn>;
};

const kinozal = {
  name: "kinozal",
  display_name: "Kinozal",
  default_domain: "kinozal.tv",
  known_domains: ["kinozal.tv", "kinozal.me"],
  custom_domains: [] as string[],
  active_domain: "",
};

const rutracker = {
  name: "rutracker",
  display_name: "RuTracker",
  default_domain: "rutracker.org",
  known_domains: ["rutracker.org", "rutracker.net"],
  custom_domains: ["mirror.example.org"],
  active_domain: "rutracker.net",
};

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TrackerDomainsCard />
    </QueryClientProvider>,
  );
  return { qc };
}

describe("TrackerDomainsCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.listTrackerDomains.mockResolvedValue([]);
  });

  it("renders a row per tracker with the active domain selected (default marked)", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal, rutracker]);
    renderCard();

    const kinozalRow = await screen.findByTestId("domain-row-kinozal");
    const kinozalSelect = within(kinozalRow).getByRole("combobox") as HTMLSelectElement;
    expect(kinozalSelect.value).toBe("");
    const kinozalOptions = within(kinozalSelect)
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(kinozalOptions[0]).toBe("kinozal.tv (default)");
    expect(kinozalOptions).toContain("kinozal.me");
    // The default domain isn't duplicated as a plain option.
    expect(kinozalOptions.filter((o) => o === "kinozal.tv")).toHaveLength(0);

    const rutrackerRow = await screen.findByTestId("domain-row-rutracker");
    expect(within(rutrackerRow).getByRole("combobox")).toHaveValue("rutracker.net");
    // Once as a select option, once as a removable custom-domain chip.
    expect(within(rutrackerRow).getAllByText("mirror.example.org")).toHaveLength(2);
  });

  it("fires an update with {active_domain, custom_domains} on select change and invalidates the query", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal]);
    mockApi.updateTrackerDomains.mockResolvedValue({ ...kinozal, active_domain: "kinozal.me" });
    const { qc } = renderCard();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

    const row = await screen.findByTestId("domain-row-kinozal");
    const select = within(row).getByRole("combobox");
    await userEvent.selectOptions(select, "kinozal.me");

    await waitFor(() =>
      expect(mockApi.updateTrackerDomains).toHaveBeenCalledWith("kinozal", {
        active_domain: "kinozal.me",
        custom_domains: [],
      }),
    );
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: QK.trackerDomains }));
  });

  it("adds a valid custom domain (appended) and rejects an invalid one client-side with no request", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal]);
    mockApi.updateTrackerDomains.mockResolvedValue({ ...kinozal, custom_domains: ["kinozal.example"] });
    renderCard();

    const row = await screen.findByTestId("domain-row-kinozal");
    const input = within(row).getByRole("textbox");
    const addButton = within(row).getByRole("button", { name: /add/i });

    await userEvent.type(input, "https://x.y");
    await userEvent.click(addButton);
    expect(mockApi.updateTrackerDomains).not.toHaveBeenCalled();
    expect(within(row).getByText(/invalid/i)).toBeInTheDocument();

    await userEvent.clear(input);
    await userEvent.type(input, "kinozal.example");
    await userEvent.click(addButton);

    await waitFor(() =>
      expect(mockApi.updateTrackerDomains).toHaveBeenCalledWith("kinozal", {
        active_domain: "",
        custom_domains: ["kinozal.example"],
      }),
    );
  });

  it("shows a save-failed message when the update mutation rejects", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal]);
    mockApi.updateTrackerDomains.mockRejectedValue(new Error("network error"));
    renderCard();

    const row = await screen.findByTestId("domain-row-kinozal");
    const select = within(row).getByRole("combobox");
    await userEvent.selectOptions(select, "kinozal.me");

    expect(await within(row).findByText(/save failed/i)).toBeInTheDocument();
  });

  it("tests the typed candidate mirror instead of the saved domain when the add input has a value", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal]);
    mockApi.testTrackerDomain.mockResolvedValue({ ok: true, detail: "" });
    renderCard();

    const row = await screen.findByTestId("domain-row-kinozal");
    const input = within(row).getByRole("textbox");
    const testButton = within(row).getByRole("button", { name: /test/i });

    await userEvent.type(input, "candidate.example");
    await userEvent.click(testButton);

    await waitFor(() =>
      expect(mockApi.testTrackerDomain).toHaveBeenCalledWith("kinozal", "candidate.example"),
    );
    expect(await within(row).findByText(/reachable/i)).toBeInTheDocument();
  });

  it("rejects an invalid typed candidate on Test client-side, with no request", async () => {
    mockApi.listTrackerDomains.mockResolvedValue([kinozal]);
    renderCard();

    const row = await screen.findByTestId("domain-row-kinozal");
    const input = within(row).getByRole("textbox");
    const testButton = within(row).getByRole("button", { name: /test/i });

    await userEvent.type(input, "https://x.y");
    await userEvent.click(testButton);

    expect(mockApi.testTrackerDomain).not.toHaveBeenCalled();
    expect(within(row).getByText(/invalid/i)).toBeInTheDocument();
  });
});

describe("SettingsPage admin gating", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.listTrackerDomains.mockResolvedValue([]);
  });

  it("does not render the tracker domains section for non-admins", async () => {
    role = "user";
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SettingsPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText("About")).toBeInTheDocument();
    expect(screen.queryByText("Tracker domains")).not.toBeInTheDocument();
    expect(mockApi.listTrackerDomains).not.toHaveBeenCalled();
  });
});
