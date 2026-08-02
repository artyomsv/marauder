import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { VerificationNotice } from "./VerificationNotice";

/**
 * The notice exists because a login that was never checked used to render
 * identically to a confirmed one. Its whole job is to make the third state
 * visible, so the tests pin exactly when it appears and when it stays out
 * of the way.
 */
describe("VerificationNotice", () => {
  it("renders nothing when the session was confirmed", () => {
    const { container } = render(<VerificationNotice verified={true} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when no login round-trip happened", () => {
    const { container } = render(<VerificationNotice verified={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("warns when the plugin could not confirm the session", () => {
    render(<VerificationNotice verified={false} />);
    expect(screen.getByRole("status")).toHaveTextContent(/could not be verified/i);
  });

});
