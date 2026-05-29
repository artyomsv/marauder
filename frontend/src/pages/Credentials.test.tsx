import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { CredentialsPage } from "./Credentials";
import { ApiError } from "@/lib/api";

// The page talks to the backend exclusively through the `api` object and
// reads the tracker list via the `useSystemInfo` hook. Mock both so the
// test exercises real rendered behaviour (captcha image, answer input,
// inline error, refresh) without a network or a running backend.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      del: vi.fn(),
      interactiveBegin: vi.fn(),
      interactiveComplete: vi.fn(),
      interactiveRefresh: vi.fn(),
    },
  };
});

vi.mock("@/lib/hooks/useSystemInfo", () => ({
  useSystemInfo: () => ({
    data: { trackers: [{ name: "lostfilm", display_name: "LostFilm" }] },
  }),
}));

import { api } from "@/lib/api";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  interactiveBegin: ReturnType<typeof vi.fn>;
  interactiveComplete: ReturnType<typeof vi.fn>;
  interactiveRefresh: ReturnType<typeof vi.fn>;
};

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return render(<CredentialsPage />, { wrapper });
}

// Fills the add-credential form and clicks "Login & save".
async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /add account/i }));
  await user.type(screen.getByLabelText(/username \/ email/i), "user@example.com");
  await user.type(screen.getByLabelText(/^password$/i), "hunter2");
  await user.click(screen.getByRole("button", { name: /login & save/i }));
}

describe("CredentialsPage — interactive captcha flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Empty credentials list so the add form's tracker is available.
    mockApi.get.mockResolvedValue({ credentials: [] });
  });

  it("shows the captcha image and answer input after begin returns a challenge", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,Rk=",
    });

    renderPage();
    await fillAndSubmit(user);

    const img = await screen.findByRole("img", { name: /captcha/i });
    expect(img).toHaveAttribute("src", "data:image/gif;base64,Rk=");
    expect(screen.getByPlaceholderText(/code from the image/i)).toBeInTheDocument();
    expect(mockApi.interactiveBegin).toHaveBeenCalledWith({
      tracker_name: "lostfilm",
      username: "user@example.com",
      password: "hunter2",
    });
  });

  it("completes successfully and invalidates the credentials list", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,Rk=",
    });
    mockApi.interactiveComplete.mockResolvedValue({ credential: { id: "x" } });

    renderPage();
    await fillAndSubmit(user);
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "ABCD");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    await waitFor(() =>
      expect(mockApi.interactiveComplete).toHaveBeenCalledWith({
        tracker_name: "lostfilm",
        challenge_id: "c1",
        answer: "ABCD",
      }),
    );
    // Form closed → captcha image gone.
    await waitFor(() =>
      expect(screen.queryByRole("img", { name: /captcha/i })).not.toBeInTheDocument(),
    );
  });

  it("shows an error and refreshes the image on a captcha_incorrect response", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,OLD=",
    });
    mockApi.interactiveComplete.mockRejectedValue(
      new ApiError({ title: "Unprocessable", status: 422, detail: "captcha_incorrect" }),
    );
    mockApi.interactiveRefresh.mockResolvedValue({
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,NEW=",
    });

    renderPage();
    await fillAndSubmit(user);
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "WRNG");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    expect(await screen.findByText(/incorrect code/i)).toBeInTheDocument();
    await waitFor(() => expect(mockApi.interactiveRefresh).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByRole("img", { name: /captcha/i })).toHaveAttribute(
        "src",
        "data:image/gif;base64,NEW=",
      ),
    );
  });

  it("falls back to the plain create flow for non-interactive trackers", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockRejectedValue(
      new ApiError({
        title: "Unprocessable",
        status: 422,
        detail: "tracker 'lostfilm' does not support interactive login",
      }),
    );
    const post = api.post as unknown as ReturnType<typeof vi.fn>;
    post.mockResolvedValue({ id: "x" });

    renderPage();
    await fillAndSubmit(user);

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/credentials", {
        tracker_name: "lostfilm",
        username: "user@example.com",
        password: "hunter2",
      }),
    );
    // No captcha UI for the fallback path.
    expect(screen.queryByRole("img", { name: /captcha/i })).not.toBeInTheDocument();
  });
});
