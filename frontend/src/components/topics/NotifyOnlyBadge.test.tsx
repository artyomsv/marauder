import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { NotifyOnlyBadge } from "./NotifyOnlyBadge";
import type { Topic } from "@/lib/api";

function topicWith(notifyOnly: boolean): Topic {
  return { NotifyOnly: notifyOnly } as unknown as Topic;
}

describe("NotifyOnlyBadge", () => {
  it("renders nothing for a normal topic", () => {
    const { container } = render(<NotifyOnlyBadge topic={topicWith(false)} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the badge for a notify-only topic", () => {
    render(<NotifyOnlyBadge topic={topicWith(true)} />);
    expect(screen.getByText("Notify only")).toBeInTheDocument();
  });
});
