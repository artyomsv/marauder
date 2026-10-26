import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      getSettings: vi.fn(),
      updateSettings: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";
import { TopicDefaultsCard } from "./TopicDefaultsCard";

const mockApi = api as unknown as {
  getSettings: ReturnType<typeof vi.fn>;
  updateSettings: ReturnType<typeof vi.fn>;
};

function wrap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

function renderCard() {
  return render(<TopicDefaultsCard />, { wrapper: wrap() });
}

beforeEach(() => {
  mockApi.getSettings.mockReset();
  mockApi.updateSettings.mockReset();
  mockApi.updateSettings.mockResolvedValue({ default_follow_tracker_title: true });
});

describe("TopicDefaultsCard", () => {
  it("shows the stored default once it is known", async () => {
    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: true });
    renderCard();
    const box = screen.getByRole("checkbox") as HTMLInputElement;
    await waitFor(() => expect(box.checked).toBe(true));
    expect(box).not.toBeDisabled();
  });

  it("keeps the control disabled until the read settles", () => {
    // Never invent a value: while the stored default is unknown the checkbox
    // is not writable, or a save would write whatever the empty state shows.
    mockApi.getSettings.mockReturnValue(new Promise(() => {}));
    renderCard();
    expect(screen.getByRole("checkbox")).toBeDisabled();
  });

  it("reports a failed read with a retry instead of a lying switch", async () => {
    mockApi.getSettings.mockRejectedValue(new Error("db down"));
    renderCard();

    await screen.findByText(/could not be read/i);
    // Withheld entirely: an enabled-but-unchecked switch here would claim the
    // default is off and happily save that over the truth.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();

    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: true });
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));
    await waitFor(() => expect(mockApi.getSettings).toHaveBeenCalledTimes(2));
    const recovered = (await screen.findByRole("checkbox")) as HTMLInputElement;
    expect(recovered.checked).toBe(true);
  });

  it("saves the chosen value", async () => {
    mockApi.getSettings.mockResolvedValue({ default_follow_tracker_title: false });
    renderCard();
    const box = screen.getByRole("checkbox") as HTMLInputElement;
    await waitFor(() => expect(box).not.toBeDisabled());
    await userEvent.click(box);
    expect(mockApi.updateSettings).toHaveBeenCalledWith({ default_follow_tracker_title: true });
  });
});
