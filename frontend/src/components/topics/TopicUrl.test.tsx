import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { TopicUrl } from "./TopicUrl";

const MAGNET =
  "magnet:?xt=urn:btih:2EEE793C09553B47290888FD97A327E9CF5E24D7&tr=http%3A%2F%2Fbt.t-ru.org%2Fann%3Fmagnet&dn=Mortal";

describe("TopicUrl", () => {
  it("renders an http URL verbatim with no copy button", () => {
    render(<TopicUrl url="https://www.lostfilm.tv/series/From" />);
    expect(screen.getByText("https://www.lostfilm.tv/series/From")).toBeInTheDocument();
    expect(screen.queryByLabelText(/copy magnet/i)).not.toBeInTheDocument();
  });

  it("collapses a magnet to its infohash form and shows a copy button", () => {
    render(<TopicUrl url={MAGNET} />);
    // Exact-match: the visible label is ONLY the canonical infohash magnet —
    // this fails if any of the noisy &tr=…&dn=… tail leaks into the text, so it
    // is a stronger check than a substring assertion (and avoids a host
    // substring match that CodeQL flags as incomplete URL sanitization).
    expect(
      screen.getByText("magnet:?xt=urn:btih:2EEE793C09553B47290888FD97A327E9CF5E24D7"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/copy magnet/i)).toBeInTheDocument();
  });
});
