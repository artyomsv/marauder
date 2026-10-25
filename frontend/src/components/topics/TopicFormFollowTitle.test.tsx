import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactElement, type ReactNode } from "react";

vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      previewTracker: vi.fn(),
      getClientCategories: vi.fn(),
    },
  };
});

import { api } from "@/lib/api";
import { TopicForm, type TopicFormValues } from "./TopicForm";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  previewTracker: ReturnType<typeof vi.fn>;
  getClientCategories: ReturnType<typeof vi.fn>;
};

function wrap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const INITIAL: TopicFormValues = {
  url: "https://tracker.example/viewtopic.php?t=1",
  displayName: "Original Name",
  quality: "",
  startSeason: "",
  startEpisode: "",
  clientId: "",
  notifierId: "",
  downloadDir: "",
  category: "",
  replaceOnUpdate: false,
  replaceDeleteData: true,
  notifyOnly: false,
  notifyOnlyAnnounceCurrent: false,
  addPausedOnUpdate: false,
  onlyNewFiles: false,
  checkIntervalSec: 900,
  folderNameMode: "default",
  folderName: "",
  followTrackerTitle: true,
};

function renderForm(onSubmit: (v: TopicFormValues) => void, initial: TopicFormValues = INITIAL) {
  return render(
    <TopicForm
      mode="edit"
      initial={initial}
      submitLabel="Save"
      heading="Edit topic"
      isPending={false}
      error={null}
      onClose={() => {}}
      onSubmit={onSubmit}
    />,
    { wrapper: wrap() },
  );
}

// Re-render the same form with a different seeded flag, standing in for the
// add card's settings query settling after mount.
function rerenderFormWith(
  rerender: (ui: ReactElement) => void,
  followTrackerTitle: boolean,
) {
  rerender(
    <TopicForm
      mode="edit"
      initial={{ ...INITIAL, followTrackerTitle }}
      submitLabel="Save"
      heading="Edit topic"
      isPending={false}
      error={null}
      onClose={() => {}}
      onSubmit={() => {}}
    />,
  );
}

beforeEach(() => {
  mockApi.get.mockReset();
  mockApi.previewTracker.mockReset();
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match"))
      return Promise.resolve({
        tracker_name: "faketracker",
        display_name: "Fake Tracker",
        supports_episode_filter: false,
        supports_season_catalog: false,
        requires_credentials: false,
        credentials_optional: false,
        uses_cloudflare: false,
      });
    if (path.startsWith("/clients")) return Promise.resolve({ clients: [] });
    if (path.startsWith("/credentials")) return Promise.resolve({ credentials: [] });
    return Promise.resolve({});
  });
  mockApi.getClientCategories.mockResolvedValue({ supported: false, categories: [] });
});

describe("TopicForm follow-the-tracker-title", () => {
  it("shows the stored flag", () => {
    renderForm(() => {});
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    expect(box.checked).toBe(true);
  });

  it("turns the toggle off while the name is edited", async () => {
    renderForm(() => {});
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    expect(box.checked).toBe(false);
    expect(box.disabled).toBe(true);
    expect(screen.getByText(/save the new name first/i)).toBeInTheDocument();
  });

  it("restores the stored choice when the name edit is reverted", async () => {
    renderForm(() => {});
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    await userEvent.type(screen.getByLabelText(/display name/i), "{Backspace}");
    expect(box.disabled).toBe(false);
    expect(box.checked).toBe(true);
  });

  it("submits the stored flag when the name is untouched", async () => {
    const onSubmit = vi.fn();
    renderForm(onSubmit);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ followTrackerTitle: true }));
  });

  it("submits the flag off after a manual rename", async () => {
    const onSubmit = vi.fn();
    renderForm(onSubmit);
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ followTrackerTitle: false, displayName: "Original Namex" }),
    );
  });

  it("hides the toggle for a feed tracker and never sends the flag on", async () => {
    // A feed tracker's "title" is the newest feed item: following it would
    // rename the topic on every check, so there is nothing to offer here.
    mockApi.get.mockImplementation((path: string) => {
      if (path.startsWith("/trackers/match"))
        return Promise.resolve({
          tracker_name: "torznab",
          display_name: "Torznab",
          supports_episode_filter: false,
          supports_season_catalog: false,
          requires_credentials: false,
          credentials_optional: false,
          uses_cloudflare: false,
          supports_title_follow: false,
        });
      return Promise.resolve({});
    });
    const onSubmit = vi.fn();
    renderForm(onSubmit);

    // The match query resolves after mount; the toggle disappears with it.
    await waitFor(() =>
      expect(screen.queryByLabelText(/follow the tracker's title/i)).not.toBeInTheDocument(),
    );

    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ followTrackerTitle: false }));
  });

  it("applies the instance default when it loads after mount", () => {
    // The add card's settings query settles after the form mounts; the
    // toggle must pick the default up rather than save an explicit false
    // over an enabled default.
    const { rerender } = renderForm(() => {}, { ...INITIAL, followTrackerTitle: false });
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    expect(box.checked).toBe(false);
    rerenderFormWith(rerender, true);
    expect(box.checked).toBe(true);
  });

  it("does not let a late default overwrite the user's choice", async () => {
    const { rerender } = renderForm(() => {}, { ...INITIAL, followTrackerTitle: false });
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    await userEvent.click(box); // explicit on
    await userEvent.click(box); // explicit off — a choice, not "unset"
    rerenderFormWith(rerender, true);
    expect(box.checked).toBe(false);
  });
});
