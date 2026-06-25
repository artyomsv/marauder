import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { pollInterval, ACTIVE_POLL_MS, IDLE_POLL_MS } from "@/components/topics/DeliveryStatus";

// The component talks to the backend only through api.topicStatus, so a
// single mock exercises every render path without a network.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { topicStatus: vi.fn() } };
});

import { api } from "@/lib/api";
import { DeliveryStatus } from "./DeliveryStatus";

const mockApi = api as unknown as { topicStatus: ReturnType<typeof vi.fn> };

function wrap() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

describe("DeliveryStatus", () => {
  it("renders nothing when there are no deliveries", async () => {
    mockApi.topicStatus.mockResolvedValue({
      client_supports_status: false,
      deliveries: [],
    });
    const { container } = render(<DeliveryStatus topicId="t1" />, {
      wrapper: wrap(),
    });
    await new Promise((r) => setTimeout(r, 0));
    expect(container.textContent).toBe("");
  });

  it("groups episodes by season with a live percentage and a finished mark", async () => {
    mockApi.topicStatus.mockResolvedValue({
      client_supports_status: true,
      deliveries: [
        {
          label: "s01e01",
          infohash: "aaa",
          delivered_at: "2026-05-30T00:00:00Z",
          state: "downloading",
          percent_done: 0.42,
        },
        {
          label: "s01e02",
          infohash: "bbb",
          delivered_at: "2026-05-30T00:00:00Z",
          state: "seeding",
          percent_done: 1,
        },
      ],
    });
    render(<DeliveryStatus topicId="t1" />, { wrapper: wrap() });

    // Season header shows done/total (e02 finished, e01 still downloading).
    expect(await screen.findByText("Season 1 · 1/2")).toBeInTheDocument();
    // Episode chips are labelled E01/E02, not the raw sNNeNN form.
    expect(await screen.findByText(/E01\s+42%/)).toBeInTheDocument();
    expect(await screen.findByText("E02")).toBeInTheDocument();
    expect(await screen.findByText("2 delivered")).toBeInTheDocument();
  });

  it("sorts episodes ascending and orders seasons even when delivered out of order", async () => {
    mockApi.topicStatus.mockResolvedValue({
      client_supports_status: false,
      deliveries: [
        { label: "s02e01", infohash: "s2e1", delivered_at: "2026-05-30T00:00:03Z", state: "delivered", percent_done: null },
        { label: "s01e03", infohash: "s1e3", delivered_at: "2026-05-30T00:00:02Z", state: "delivered", percent_done: null },
        { label: "s01e01", infohash: "s1e1", delivered_at: "2026-05-30T00:00:01Z", state: "delivered", percent_done: null },
      ],
    });
    const { container } = render(<DeliveryStatus topicId="t1" />, { wrapper: wrap() });

    await screen.findByText("Season 1 · 2/2");
    // Two season headers (1 then 2), and episode chips in ascending order.
    const text = container.textContent ?? "";
    expect(text.indexOf("Season 1")).toBeLessThan(text.indexOf("Season 2"));
    expect(text.indexOf("E01")).toBeLessThan(text.indexOf("E03"));
    expect(await screen.findByText("Season 2 · 1/1")).toBeInTheDocument();
  });

  it("shows delivered-only labels when the client lacks live status", async () => {
    mockApi.topicStatus.mockResolvedValue({
      client_supports_status: false,
      deliveries: [
        {
          label: "Some.Release.1080p",
          infohash: "ccc",
          delivered_at: "2026-05-30T00:00:00Z",
          state: "delivered",
          percent_done: null,
        },
      ],
    });
    render(<DeliveryStatus topicId="t1" />, { wrapper: wrap() });

    expect(await screen.findByText("Some.Release.1080p")).toBeInTheDocument();
  });
});

describe("pollInterval", () => {
  const downloading = [{ state: "downloading" }];
  const idle = [{ state: "seeding" }];

  it("disables polling while SSE is connected", () => {
    expect(pollInterval(true, downloading)).toBe(false);
    expect(pollInterval(true, idle)).toBe(false);
  });

  it("polls fast when disconnected and a delivery is active", () => {
    expect(pollInterval(false, downloading)).toBe(ACTIVE_POLL_MS);
  });

  it("polls slow when disconnected and nothing is active", () => {
    expect(pollInterval(false, idle)).toBe(IDLE_POLL_MS);
  });
});
