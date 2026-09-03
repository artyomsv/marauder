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
  post: ReturnType<typeof vi.fn>;
  previewTracker: ReturnType<typeof vi.fn>;
  getClientCategories: ReturnType<typeof vi.fn>;
};

function wrap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const EMPTY: TopicFormValues = {
  url: "",
  displayName: "",
  quality: "",
  startSeason: "",
  startEpisode: "",
  clientId: "",
  notifierId: "",
  downloadDir: "",
  category: "",
  replaceOnUpdate: false,
  replaceDeleteData: true,
};

function renderForm() {
  render(
    <TopicForm
      mode="add"
      initial={EMPTY}
      submitLabel="Add topic"
      heading="Add a new topic"
      isPending={false}
      error={null}
      onClose={() => {}}
      onSubmit={() => {}}
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
        tracker_name: "toloka",
        display_name: "Toloka.to",
        supports_episode_filter: false,
        supports_season_catalog: false,
        requires_credentials: true,
        credentials_optional: false,
        uses_cloudflare: false,
      });
    if (path.startsWith("/clients")) return Promise.resolve({ clients: [] });
    if (path.startsWith("/credentials")) return Promise.resolve({ credentials: [] });
    return Promise.resolve({});
  });
  mockApi.getClientCategories.mockResolvedValue({ supported: false, categories: [] });
});

describe("TopicForm resolving indicators", () => {
  // Resolving a preview can take seconds — a login-gated tracker warms a
  // session first — and with nothing on screen the form looked inert.
  it("shows a placeholder card while the preview is still resolving", async () => {
    // Never resolves: the form must show the pending state, not nothing.
    mockApi.previewTracker.mockImplementation(() => new Promise(() => {}));
    renderForm();

    await userEvent.type(
      screen.getByLabelText(/url or magnet link/i),
      "https://toloka.to/t33571",
    );

    expect(await screen.findByText(/resolving title and poster/i)).toBeInTheDocument();
  });

  it("swaps the placeholder for the resolved title once it arrives", async () => {
    mockApi.previewTracker.mockResolvedValue({
      title: "Микола Кондратюк - На долині туман (2004) [FLAC] | Pop",
      image_url: "https://thumb.hurtom.com/image/w250/x.jpg",
    });
    renderForm();

    await userEvent.type(
      screen.getByLabelText(/url or magnet link/i),
      "https://toloka.to/t33571",
    );

    expect(await screen.findByText(/Микола Кондратюк/)).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByText(/resolving title and poster/i)).not.toBeInTheDocument(),
    );
  });
});
