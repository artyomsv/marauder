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
      get: vi.fn(),
      post: vi.fn(),
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

// A topic the API accepts: only-new-files is on, replace-on-update is off,
// and replace_delete_data kept its default of true (the 422 rule only
// applies when all three are on).
const STORED: TopicFormValues = {
  url: "https://toloka.to/t33571",
  displayName: "TEST-205 topic",
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
  onlyNewFiles: true,
  checkIntervalSec: 900,
};

beforeEach(() => {
  mockApi.get.mockReset();
  mockApi.get.mockImplementation((path: string) => {
    if (path.startsWith("/trackers/match"))
      return Promise.resolve({
        tracker_name: "toloka",
        display_name: "Toloka.to",
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
  mockApi.previewTracker.mockResolvedValue({ title: "", image_url: "" });
  mockApi.getClientCategories.mockResolvedValue({ supported: false, categories: [] });
});

describe("TopicForm update policy submit", () => {
  // The delete-data box shows unticked while only-new-files is on, so the
  // form must submit what it shows: sending the hidden stored `true` with
  // replace-on-update turned on is the combination the API rejects with 422.
  it("submits replaceDeleteData false when only-new-files is on", async () => {
    const onSubmit = vi.fn();
    render(
      <TopicForm
        mode="edit"
        initial={STORED}
        submitLabel="Save"
        heading="Edit topic"
        isPending={false}
        error={null}
        onClose={() => {}}
        onSubmit={onSubmit}
      />,
      { wrapper: wrap() },
    );

    await userEvent.click(screen.getByLabelText(/replace previous version on update/i));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      replaceOnUpdate: true,
      onlyNewFiles: true,
      replaceDeleteData: false,
    });
  });
});
