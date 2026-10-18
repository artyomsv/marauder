import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { useAuthStore } from "@/lib/auth-store";
import type { Me } from "@/lib/api";
import { MobileNav } from "./MobileNav";

// The sign-out button calls useLogout, which POSTs to /auth/logout. No test
// here presses it, but the module is imported, so keep the network out.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return { ...actual, api: { post: vi.fn() } };
});

function setUser(role: Me["role"]) {
  useAuthStore.setState({
    user: { id: "u1", username: "alice", email: "a@example.com", role },
  });
}

function renderNav() {
  render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="*" element={<MobileNav />} />
      </Routes>
    </MemoryRouter>,
  );
}

async function openMenu(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /open menu/i }));
  return screen.getByRole("dialog", { name: /menu/i });
}

/**
 * jsdom has no matchMedia. Install one that records the "change" listener so
 * a test can fire a viewport crossing by hand.
 */
function stubMatchMedia() {
  const listeners: Array<(e: MediaQueryListEvent) => void> = [];
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches: false,
      addEventListener: (_: string, cb: (e: MediaQueryListEvent) => void) => {
        listeners.push(cb);
      },
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
    })),
  );
  return {
    crossToDesktop: () =>
      listeners.forEach((cb) => cb({ matches: true } as MediaQueryListEvent)),
  };
}

describe("MobileNav", () => {
  beforeEach(() => {
    setUser("user");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps the panel closed until the menu button is pressed", async () => {
    const user = userEvent.setup();
    renderNav();

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    const button = screen.getByRole("button", { name: /open menu/i });
    expect(button).toHaveAttribute("aria-expanded", "false");

    await user.click(button);

    const panel = screen.getByRole("dialog", { name: /menu/i });
    expect(panel).toBeInTheDocument();
    expect(button).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: /topics/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /sign out/i })).toBeInTheDocument();
  });

  it("moves keyboard focus into the panel and back to the button on close", async () => {
    const user = userEvent.setup();
    renderNav();
    const button = screen.getByRole("button", { name: /open menu/i });

    await user.click(button);
    const panel = screen.getByRole("dialog", { name: /menu/i });
    expect(panel.contains(document.activeElement)).toBe(true);

    await user.keyboard("{Escape}");
    expect(button).toHaveFocus();
  });

  it("hides admin-only pages from a non-admin user", async () => {
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    expect(screen.queryByRole("link", { name: /audit log/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /integrations/i })).not.toBeInTheDocument();
  });

  it("shows admin-only pages to an admin", async () => {
    setUser("admin");
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    expect(screen.getByRole("link", { name: /audit log/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /integrations/i })).toBeInTheDocument();
  });

  it("closes when a page link is pressed", async () => {
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    await user.click(screen.getByRole("link", { name: /topics/i }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes when the close button is pressed", async () => {
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    await user.click(screen.getByRole("button", { name: /close menu/i }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes when the backdrop is pressed", async () => {
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    await user.click(screen.getByTestId("mobile-nav-backdrop"));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes when the viewport grows past the md breakpoint", async () => {
    const media = stubMatchMedia();
    const user = userEvent.setup();
    renderNav();
    await openMenu(user);

    act(() => media.crossToDesktop());

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
