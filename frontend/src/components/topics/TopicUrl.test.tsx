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
    expect(
      screen.getByText("magnet:?xt=urn:btih:2EEE793C09553B47290888FD97A327E9CF5E24D7"),
    ).toBeInTheDocument();
    // The noisy tr=/dn= params are dropped from the visible label.
    expect(screen.queryByText(/bt\.t-ru\.org/)).not.toBeInTheDocument();
    expect(screen.getByLabelText(/copy magnet/i)).toBeInTheDocument();
  });
});
