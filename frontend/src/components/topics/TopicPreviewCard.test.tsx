import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { TopicPreviewCard } from "./TopicPreviewCard";

describe("TopicPreviewCard", () => {
  it("renders nothing when there is neither a lookup nor a result", () => {
    const { container } = render(<TopicPreviewCard preview={null} pending={false} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for a result that resolved to no title and no poster", () => {
    // The preview endpoint fails open with empty fields rather than erroring,
    // so an empty object arrives on every tracker that can't resolve anything.
    // Showing an empty card for it would be worse than showing no card.
    const { container } = render(
      <TopicPreviewCard preview={{ title: "", image_url: "" }} pending={false} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the skeleton while the lookup is in flight", () => {
    render(<TopicPreviewCard preview={null} pending />);
    expect(screen.getByText(/resolving title and poster/i)).toBeInTheDocument();
  });

  it("shows a ghost box when a resolved title carries no poster", () => {
    // A silently absent poster and a poster that failed to load look identical
    // when the element just disappears, which made a real bug hard to tell
    // from a tracker that simply has no artwork.
    render(<TopicPreviewCard preview={{ title: "Some Release", image_url: "" }} pending={false} />);
    expect(screen.getByText("Some Release")).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(document.querySelector('[aria-hidden="true"]')).toBeInTheDocument();
  });

  it("shows the poster when one resolved", () => {
    render(
      <TopicPreviewCard
        preview={{ title: "Some Release", image_url: "https://thumb.hurtom.com/image/w250/x.jpg" }}
        pending={false}
      />,
    );
    const img = screen.getByRole("img");
    expect(img).toHaveAttribute("src", "https://thumb.hurtom.com/image/w250/x.jpg");
    expect(screen.queryByText(/resolving title and poster/i)).not.toBeInTheDocument();
  });

  it("shows a title-only card as an em dash rather than a blank line", () => {
    render(
      <TopicPreviewCard
        preview={{ title: "", image_url: "https://thumb.hurtom.com/image/w250/x.jpg" }}
        pending={false}
      />,
    );
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
