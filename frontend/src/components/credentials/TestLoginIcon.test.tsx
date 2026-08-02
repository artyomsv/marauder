import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { TestLoginIcon } from "./TestLoginIcon";

/**
 * The whole point of this icon is that "verified" and "unverified" must not
 * look alike — an unchecked login rendering the same green tick as a
 * confirmed one is the bug this component exists to prevent.
 */
describe("TestLoginIcon", () => {
  it("distinguishes an unverified result from a verified one", () => {
    const { unmount } = render(<TestLoginIcon phase="verified" />);
    const verifiedLabel = screen.getByRole("img").getAttribute("aria-label");
    unmount();

    render(<TestLoginIcon phase="unverified" />);
    const unverifiedLabel = screen.getByRole("img").getAttribute("aria-label");

    expect(unverifiedLabel).not.toBe(verifiedLabel);
  });

  it("labels the unverified result so it is not read as success", () => {
    render(<TestLoginIcon phase="unverified" />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", expect.stringMatching(/unverified/i));
  });

  it("labels a failed result distinctly from an unverified one", () => {
    render(<TestLoginIcon phase="failed" />);
    expect(screen.getByRole("img").getAttribute("aria-label")).not.toMatch(/unverified/i);
  });
});
