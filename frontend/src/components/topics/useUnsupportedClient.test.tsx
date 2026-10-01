import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { get: vi.fn() } };
});

import { api } from "@/lib/api";
import { useUnsupportedClient } from "./useUnsupportedClient";

const mockApi = api as unknown as { get: ReturnType<typeof vi.fn> };

const CLIENTS = [
  { id: "c1", display_name: "Box A", client_name: "utorrent" },
  { id: "c2", display_name: "Box B", client_name: "qbittorrent" },
];

function run(clientId: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(() => useUnsupportedClient(CLIENTS, clientId), { wrapper });
}

describe("useUnsupportedClient", () => {
  beforeEach(() => {
    mockApi.get.mockReset();
    mockApi.get.mockResolvedValue({
      clients: [
        { name: "utorrent", display_name: "µTorrent", supports_file_selection: false },
        { name: "qbittorrent", display_name: "qBittorrent", supports_file_selection: true },
      ],
    });
  });

  it("returns the display name of a client without file selection", async () => {
    const { result } = run("c1");
    await waitFor(() => expect(result.current).toBe("Box A"));
  });

  it("returns null for a client that supports file selection", async () => {
    const { result } = run("c2");
    await waitFor(() => expect(mockApi.get).toHaveBeenCalled());
    await waitFor(() => expect(result.current).toBeNull());
  });

  it("returns null for an unknown or empty client id", async () => {
    const a = run("nope");
    const b = run("");
    await waitFor(() => expect(mockApi.get).toHaveBeenCalled());
    expect(a.result.current).toBeNull();
    expect(b.result.current).toBeNull();
  });

  it("returns null when system info has no clients", async () => {
    mockApi.get.mockResolvedValue({});
    const { result } = run("c1");
    await waitFor(() => expect(mockApi.get).toHaveBeenCalled());
    expect(result.current).toBeNull();
  });
});
