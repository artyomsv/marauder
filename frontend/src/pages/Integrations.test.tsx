import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

// SonarrCard (rendered for admins) talks to the API + useSystemInfo — mock both.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn().mockResolvedValue({ clients: [] }),
      getSonarrConfig: vi.fn().mockResolvedValue({
        enabled: false,
        sonarr_url: "",
        api_key_set: false,
        poll_interval_sec: 900,
        allowed_trackers: [],
        default_client_id: null,
        default_category: "",
        default_download_dir: "",
        update_existing: false,
        last_seen_at: null,
      }),
      updateSonarrConfig: vi.fn(),
      testSonarr: vi.fn(),
    },
  };
});

vi.mock("@/lib/hooks/useSystemInfo", () => ({
  useSystemInfo: () => ({ data: { trackers: [] } }),
}));

// Drives the admin-gate per test.
let role: "admin" | "user" = "admin";
vi.mock("@/lib/auth-store", () => ({
  useAuthStore: (sel: (s: { user: { id: string; username: string; email: string; role: string } }) => unknown) =>
    sel({ user: { id: "u1", username: "x", email: "", role } }),
}));

import { IntegrationsPage } from "./Integrations";

function renderAt(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/integrations"]}>
        <Routes>
          <Route path="/" element={<div>HOME</div>} />
          <Route path="/integrations" element={node} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("IntegrationsPage", () => {
  it("redirects non-admins to home", async () => {
    role = "user";
    renderAt(<IntegrationsPage />);
    expect(await screen.findByText("HOME")).toBeInTheDocument();
  });

  it("renders the Sonarr card for admins", async () => {
    role = "admin";
    renderAt(<IntegrationsPage />);
    expect(await screen.findByText(/Sonarr integration/i)).toBeInTheDocument();
    expect(screen.queryByText("HOME")).not.toBeInTheDocument();
  });
});
