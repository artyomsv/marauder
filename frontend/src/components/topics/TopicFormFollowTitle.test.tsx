import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";

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

function renderForm(onSubmit: (v: TopicFormValues) => void) {
  render(
    <TopicForm
      mode="edit"
      initial={INITIAL}
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

  it("turns the toggle off when the user types a name", async () => {
    renderForm(() => {});
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    expect(box.checked).toBe(false);
  });

  it("lets the user turn it back on after editing the name", async () => {
    renderForm(() => {});
    const box = screen.getByLabelText(/follow the tracker's title/i) as HTMLInputElement;
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    await userEvent.click(box);
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
});
