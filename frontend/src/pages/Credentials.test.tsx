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
      reauthBegin: vi.fn(),
      reauthComplete: vi.fn(),
    },
  };
});

// Each test sets the selected tracker's interactive-login capability via
// this flag; the form gates the captcha flow on it (sourced by name from
// the /system/info tracker list, same list that populates the dropdown).
let supportsInteractiveLogin = true;

vi.mock("@/lib/hooks/useSystemInfo", () => ({
  useSystemInfo: () => ({
    data: {
      trackers: [
        {
          name: "lostfilm",
          display_name: "LostFilm",
          supports_interactive_login: supportsInteractiveLogin,
        },
      ],
    },
  }),
}));

import { api } from "@/lib/api";

const mockApi = api as unknown as {
  get: ReturnType<typeof vi.fn>;
  interactiveBegin: ReturnType<typeof vi.fn>;
  interactiveComplete: ReturnType<typeof vi.fn>;
  interactiveRefresh: ReturnType<typeof vi.fn>;
  reauthBegin: ReturnType<typeof vi.fn>;
  reauthComplete: ReturnType<typeof vi.fn>;
};

// A credential row whose server-side session expired — drives the
// re-authenticate badge + captcha-only dialog tests.
const expiredCredential = {
  id: "cred-1",
  tracker_name: "lostfilm",
  display_name: "My LostFilm",
  username: "user@example.com",
  session_expired: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
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
    // Default to an interactive-capable tracker; the fallback test flips it.
    supportsInteractiveLogin = true;
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

  it("disables the submit button while the auto-refresh is in flight", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,OLD=",
    });
    mockApi.interactiveComplete.mockRejectedValue(
      new ApiError({ title: "Unprocessable", status: 422, detail: "captcha_incorrect" }),
    );
    // Deferred refresh: stays pending until we resolve it, so we can
    // observe the in-flight loading/disabled state deterministically.
    let resolveRefresh!: (v: { challenge_id: string; captcha_image: string }) => void;
    mockApi.interactiveRefresh.mockReturnValue(
      new Promise((resolve) => {
        resolveRefresh = resolve;
      }),
    );

    renderPage();
    await fillAndSubmit(user);
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "WRNG");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    // Wrong answer accepted, refresh kicked off but not yet resolved:
    // the submit button must be disabled (refresh.isPending) so the user
    // can't re-submit against a challenge that's about to be replaced.
    await waitFor(() => expect(mockApi.interactiveRefresh).toHaveBeenCalled());
    expect(screen.getByRole("button", { name: /verify & save/i })).toBeDisabled();

    // Resolving the refresh clears answer and re-enables submission.
    resolveRefresh({ challenge_id: "c1", captcha_image: "data:image/gif;base64,NEW=" });
    await waitFor(() =>
      expect(screen.getByRole("img", { name: /captcha/i })).toHaveAttribute(
        "src",
        "data:image/gif;base64,NEW=",
      ),
    );
  });

  it("surfaces an error when the auto-refresh itself fails", async () => {
    const user = userEvent.setup();
    mockApi.interactiveBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "c1",
      captcha_image: "data:image/gif;base64,OLD=",
    });
    mockApi.interactiveComplete.mockRejectedValue(
      new ApiError({ title: "Unprocessable", status: 422, detail: "captcha_incorrect" }),
    );
    mockApi.interactiveRefresh.mockRejectedValue(
      new ApiError({ title: "Bad Gateway", status: 502, detail: "tracker unreachable" }),
    );

    renderPage();
    await fillAndSubmit(user);
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "WRNG");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    // The failed refresh routes through refresh.onError → error slot.
    expect(await screen.findByText(/tracker unreachable/i)).toBeInTheDocument();
  });

  it("uses the plain create flow directly for non-interactive trackers", async () => {
    supportsInteractiveLogin = false;
    const user = userEvent.setup();
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
    // Gated on the flag: the interactive begin endpoint is never called,
    // and no captcha UI appears.
    expect(mockApi.interactiveBegin).not.toHaveBeenCalled();
    expect(screen.queryByRole("img", { name: /captcha/i })).not.toBeInTheDocument();
  });
});

describe("CredentialsPage — session-expired re-authentication", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    supportsInteractiveLogin = true;
    mockApi.get.mockResolvedValue({ credentials: [expiredCredential] });
  });

  it("shows the session-expired badge and a Re-authenticate button", async () => {
    renderPage();
    expect(await screen.findByText(/session expired/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /re-authenticate/i }),
    ).toBeInTheDocument();
  });

  it("opens a captcha-only dialog with NO email/password inputs", async () => {
    const user = userEvent.setup();
    mockApi.reauthBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "rc1",
      captcha_image: "data:image/gif;base64,Rk=",
    });

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: /re-authenticate/i }),
    );

    const img = await screen.findByRole("img", { name: /captcha/i });
    expect(img).toHaveAttribute("src", "data:image/gif;base64,Rk=");
    expect(mockApi.reauthBegin).toHaveBeenCalledWith("cred-1");
    // Captcha-only: the re-auth flow must never ask for credentials again.
    expect(screen.getByPlaceholderText(/code from the image/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/username \/ email/i)).not.toBeInTheDocument();
  });

  it("closes and invalidates when begin returns logged_in", async () => {
    const user = userEvent.setup();
    mockApi.reauthBegin.mockResolvedValue({ status: "logged_in" });

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: /re-authenticate/i }),
    );

    await waitFor(() => expect(mockApi.reauthBegin).toHaveBeenCalledWith("cred-1"));
    // No captcha shown, list re-fetched (get called again on invalidation).
    expect(screen.queryByRole("img", { name: /captcha/i })).not.toBeInTheDocument();
    await waitFor(() => expect(mockApi.get.mock.calls.length).toBeGreaterThan(1));
  });

  it("completes a correct captcha answer, then closes + invalidates", async () => {
    const user = userEvent.setup();
    mockApi.reauthBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "rc1",
      captcha_image: "data:image/gif;base64,Rk=",
    });
    mockApi.reauthComplete.mockResolvedValue({ credential: expiredCredential });

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: /re-authenticate/i }),
    );
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "ABCD");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    await waitFor(() =>
      expect(mockApi.reauthComplete).toHaveBeenCalledWith("cred-1", {
        challenge_id: "rc1",
        answer: "ABCD",
      }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("img", { name: /captcha/i })).not.toBeInTheDocument(),
    );
  });

  it("shows an error and refreshes the image on captcha_incorrect", async () => {
    const user = userEvent.setup();
    mockApi.reauthBegin.mockResolvedValue({
      status: "captcha",
      challenge_id: "rc1",
      captcha_image: "data:image/gif;base64,OLD=",
    });
    mockApi.reauthComplete.mockRejectedValue(
      new ApiError({ title: "Unprocessable", status: 422, detail: "captcha_incorrect" }),
    );
    mockApi.interactiveRefresh.mockResolvedValue({
      challenge_id: "rc1",
      captcha_image: "data:image/gif;base64,NEW=",
    });

    renderPage();
    await user.click(
      await screen.findByRole("button", { name: /re-authenticate/i }),
    );
    await screen.findByRole("img", { name: /captcha/i });

    await user.type(screen.getByPlaceholderText(/code from the image/i), "WRNG");
    await user.click(screen.getByRole("button", { name: /verify & save/i }));

    expect(await screen.findByText(/incorrect code/i)).toBeInTheDocument();
    await waitFor(() =>
      expect(mockApi.interactiveRefresh).toHaveBeenCalledWith({
        tracker_name: "lostfilm",
        challenge_id: "rc1",
      }),
    );
    await waitFor(() =>
      expect(screen.getByRole("img", { name: /captcha/i })).toHaveAttribute(
        "src",
        "data:image/gif;base64,NEW=",
      ),
    );
  });
});
