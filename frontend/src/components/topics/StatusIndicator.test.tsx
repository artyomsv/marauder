import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";

import { StatusIndicator } from "@/components/topics/StatusIndicator";

describe("StatusIndicator", () => {
  it("pulses only for an errored topic", () => {
    const { container: errored } = render(<StatusIndicator status="error" />);
    expect(errored.querySelector(".animate-ping")).not.toBeNull();
  });

  it.each(["active", "paused"] as const)("does not pulse for a %s topic", (status) => {
    const { container } = render(<StatusIndicator status={status} />);
    expect(container.querySelector(".animate-ping")).toBeNull();
  });
});
