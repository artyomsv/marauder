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

import { api, type UpdateSettingsBody } from "@/lib/api";
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

const DEFAULTS = { default_follow_tracker_title: true, default_replace_on_update: true, default_replace_delete_data: false };

beforeEach(() => {
  mockApi.getSettings.mockReset();
  mockApi.updateSettings.mockReset();
  mockApi.updateSettings.mockResolvedValue(DEFAULTS);
});

describe("TopicDefaultsCard", () => {
  it("shows the stored default once it is known", async () => {
    mockApi.getSettings.mockResolvedValue(DEFAULTS);
    renderCard();
    const box = screen.getByRole("checkbox", { name: /follow the tracker/i }) as HTMLInputElement;
    await waitFor(() => expect(box.checked).toBe(true));
    expect(box).not.toBeDisabled();
  });

  it("keeps the control disabled until the read settles", () => {
    // Never invent a value: while the stored default is unknown the checkbox
    // is not writable, or a save would write whatever the empty state shows.
    mockApi.getSettings.mockReturnValue(new Promise(() => {}));
    renderCard();
    expect(screen.getAllByRole("checkbox")).toHaveLength(3);
    screen.getAllByRole("checkbox").forEach((box) => expect(box).toBeDisabled());
  });

  it("reports a failed read with a retry instead of a lying switch", async () => {
    mockApi.getSettings.mockRejectedValue(new Error("db down"));
    renderCard();

    await screen.findByText(/could not be read/i);
    // Withheld entirely: an enabled-but-unchecked switch here would claim the
    // default is off and happily save that over the truth.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();

    mockApi.getSettings.mockResolvedValue(DEFAULTS);
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));
    await waitFor(() => expect(mockApi.getSettings).toHaveBeenCalledTimes(2));
    const recovered = (await screen.findByRole("checkbox", { name: /follow the tracker/i })) as HTMLInputElement;
    expect(recovered.checked).toBe(true);
  });

  it("saves the chosen value", async () => {
    mockApi.getSettings.mockResolvedValue({ ...DEFAULTS, default_follow_tracker_title: false });
    renderCard();
    const box = screen.getByRole("checkbox", { name: /follow the tracker/i }) as HTMLInputElement;
    await waitFor(() => expect(box).not.toBeDisabled());
    await userEvent.click(box);
    expect(mockApi.updateSettings).toHaveBeenCalledWith({ default_follow_tracker_title: true });
  });
});

// Each click is a patch: unrelated defaults survive and the returned full row
// becomes the source of truth for the next click.
describe("replace defaults", () => {
  it("disables deleting files while replace is off", async () => {
    mockApi.getSettings.mockResolvedValue({ ...DEFAULTS, default_replace_on_update: false });
    renderCard();
    const replace = screen.getByRole("checkbox", { name: /replace previous version/i });
    await waitFor(() => expect(replace).not.toBeDisabled());
    expect(screen.getAllByRole("checkbox")).toHaveLength(3);
    expect(screen.getByRole("checkbox", { name: /also delete/i })).toBeDisabled();
  });

  it.each([
    [/follow the tracker/i, "default_follow_tracker_title", false],
    [/replace previous version/i, "default_replace_on_update", false],
    [/also delete/i, "default_replace_delete_data", true],
  ] as const)("patches only the clicked field %s", async (label, key, value) => {
    mockApi.getSettings.mockResolvedValue(DEFAULTS);
    mockApi.updateSettings.mockImplementation((patch: UpdateSettingsBody) => Promise.resolve({ ...DEFAULTS, ...patch }));
    renderCard();
    const box = screen.getByRole("checkbox", { name: label });
    await waitFor(() => expect(box).not.toBeDisabled());
    await userEvent.click(box);
    expect(mockApi.updateSettings).toHaveBeenCalledWith({ [key]: value });
    await waitFor(() => expect((box as HTMLInputElement).checked).toBe(value));
    if (key !== "default_follow_tracker_title") {
      expect(screen.getByRole("checkbox", { name: /follow the tracker/i })).toBeChecked();
    }
  });
});
