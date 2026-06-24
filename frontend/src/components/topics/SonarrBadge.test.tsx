import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { SonarrBadge } from "./SonarrBadge";
import type { Topic } from "@/lib/api";

const topicWith = (extra: Topic["Extra"]) => ({ Extra: extra }) as Topic;

describe("SonarrBadge", () => {
  it("renders for topics auto-imported from Sonarr", () => {
    render(<SonarrBadge topic={topicWith({ source: "sonarr" })} />);
    expect(screen.getByText("Sonarr")).toBeInTheDocument();
  });

  it("renders nothing for a manually-added topic", () => {
    const { container } = render(<SonarrBadge topic={topicWith({ quality: "1080p" })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when Extra is null", () => {
    const { container } = render(<SonarrBadge topic={topicWith(null)} />);
    expect(container).toBeEmptyDOMElement();
  });
});
