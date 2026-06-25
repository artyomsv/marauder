import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { TopicCheckStatus } from "@/components/topics/TopicCheckStatus";
import { useCheckStatus } from "@/lib/check-status";

beforeEach(() => useCheckStatus.setState({ byTopic: {} }));

describe("TopicCheckStatus", () => {
  it("shows a checking indicator while checking", () => {
    useCheckStatus.getState().setChecking("t1");
    render(<TopicCheckStatus topicId="t1" />);
    expect(screen.getByText(/checking/i)).toBeInTheDocument();
  });

  it("renders nothing when there is no live status for the topic", () => {
    const { container } = render(<TopicCheckStatus topicId="unknown" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows an error indicator on failure", () => {
    useCheckStatus.getState().setFailed("t2", "boom");
    render(<TopicCheckStatus topicId="t2" />);
    expect(screen.getByText(/error/i)).toBeInTheDocument();
  });
});
