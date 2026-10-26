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
      updateTopic: vi.fn(),
    },
  };
});

import { api, type Topic } from "@/lib/api";
import { EditTopicCard } from "./EditTopicCard";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  previewTracker: ReturnType<typeof vi.fn>;
  getClientCategories: ReturnType<typeof vi.fn>;
  updateTopic: ReturnType<typeof vi.fn>;
};

function wrap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const TOPIC: Topic = {
  ID: "t-1",
  UserID: "u-1",
  TrackerName: "faketracker",
  URL: "https://tracker.example/viewtopic.php?t=1",
  DisplayName: "Tracker Title B",
  ImageURL: "",
  ClientID: null,
  NotifierID: null,
  DownloadDir: "",
  Category: "",
  ReplaceOnUpdate: false,
  ReplaceDeleteData: true,
  Extra: {},
  LastHash: "",
  NotifyOnly: false,
  NotifyOnlyAnnounceCurrent: false,
  AddPausedOnUpdate: false,
  OnlyNewFiles: false,
  CheckIntervalSec: 900,
  LastCheckedAt: null,
  LastUpdatedAt: null,
  NextCheckAt: "",
  ConsecutiveErrors: 0,
  Status: "active",
  LastError: "",
  LastErrorCode: "",
  CreatedAt: "",
  UpdatedAt: "",
  FolderNameMode: "default",
  FolderName: "",
  FolderNameSeen: "",
  FollowTrackerTitle: true,
};

beforeEach(() => {
  mockApi.get.mockReset();
  mockApi.updateTopic.mockReset();
  mockApi.updateTopic.mockResolvedValue(TOPIC);
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

describe("EditTopicCard payload", () => {
  it("omits the name and the flag when the user changed neither", async () => {
    // The M-1 property: a save of an unrelated field must not carry a stale
    // name (which the store would read as a rename) or a stale flag (which it
    // would write back over one a concurrent rename cleared).
    render(<EditTopicCard topic={TOPIC} onClose={() => {}} onSaved={() => {}} />, {
      wrapper: wrap(),
    });
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(mockApi.updateTopic).toHaveBeenCalledTimes(1);
    const [id, body] = mockApi.updateTopic.mock.calls[0];
    expect(id).toBe("t-1");
    expect(body.display_name).toBeUndefined();
    expect(body.follow_tracker_title).toBeUndefined();
  });

  it("sends the name when it was edited", async () => {
    render(<EditTopicCard topic={TOPIC} onClose={() => {}} onSaved={() => {}} />, {
      wrapper: wrap(),
    });
    await userEvent.type(screen.getByLabelText(/display name/i), "x");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    const [, body] = mockApi.updateTopic.mock.calls[0];
    expect(body.display_name).toBe("Tracker Title Bx");
  });
});
