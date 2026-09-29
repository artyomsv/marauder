import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CheckIntervalBadge } from "./CheckIntervalBadge";
import type { Topic } from "@/lib/api";

function topicWith(sec: number): Topic {
  return { CheckIntervalSec: sec } as unknown as Topic;
}

describe("CheckIntervalBadge", () => {
  it("shows the interval with a descriptive title", () => {
    render(<CheckIntervalBadge topic={topicWith(21600)} />);
    expect(screen.getByText("6 h")).toBeInTheDocument();
    expect(screen.getByTitle("Checked every 6 h")).toBeInTheDocument();
  });

  it("renders nothing without an interval", () => {
    const { container } = render(<CheckIntervalBadge topic={topicWith(0)} />);
    expect(container).toBeEmptyDOMElement();
  });
});
