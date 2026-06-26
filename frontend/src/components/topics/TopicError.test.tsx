import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { TopicError } from "./TopicError";
import type { Topic } from "@/lib/api";

const topicWith = (lastError: string, lastErrorCode: string) =>
  ({ LastError: lastError, LastErrorCode: lastErrorCode }) as Topic;

const RAW = `kinozal GET: Get "https://kinozal.tv/details.php?id=1": context deadline exceeded`;

describe("TopicError", () => {
  it("renders the friendly message for a known code", () => {
    render(<TopicError topic={topicWith(RAW, "timeout")} />);
    expect(screen.getByText(/didn't respond in time/i)).toBeInTheDocument();
    // Raw error stays available for debugging in the tooltip.
    expect(screen.getByTitle(RAW)).toBeInTheDocument();
  });

  it("falls back to the raw error when the code is unknown", () => {
    render(<TopicError topic={topicWith(RAW, "unknown")} />);
    expect(screen.getByText(RAW)).toBeInTheDocument();
  });

  it("falls back to the raw error when the code is absent (older rows)", () => {
    render(<TopicError topic={topicWith(RAW, "")} />);
    expect(screen.getByText(RAW)).toBeInTheDocument();
  });

  it("renders nothing when there is no error", () => {
    const { container } = render(<TopicError topic={topicWith("", "")} />);
    expect(container).toBeEmptyDOMElement();
  });
});
