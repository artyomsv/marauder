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

  // The code -> i18n-key map is a hand-maintained third copy of the backend's
  // errCode* vocabulary, and tsc cannot catch a typo in it because CODE_KEYS is
  // a plain Record<string, string> and a missing key silently falls through to
  // the raw error. These two cases pin the newer codes so a rename or typo in
  // either the map or the dictionaries fails a test instead of quietly
  // degrading the UI to a stack-trace-looking string.
  it("renders the friendly message for a solver failure", () => {
    render(<TopicError topic={topicWith(RAW, "solver")} />);
    expect(screen.getByText(/Cloudflare solver/i)).toBeInTheDocument();
    expect(screen.queryByText(RAW)).toBeNull();
  });

  it("renders the friendly message for a cloudflare block", () => {
    render(<TopicError topic={topicWith(RAW, "cloudflare")} />);
    expect(screen.getByText(/Blocked by Cloudflare/i)).toBeInTheDocument();
    expect(screen.queryByText(RAW)).toBeNull();
  });

  // Issue #158. The reporter ran a stack that never wired a solver up, saw
  // "this tracker needs a browser to get through", and waited for a browser
  // window. Two things must hold: an unconfigured solver gets its own message
  // naming what to run, and no Cloudflare message promises a browser Marauder
  // has no way to open.
  it("names the missing solver instead of blaming the tracker", () => {
    render(<TopicError topic={topicWith(RAW, "solver_missing")} />);
    expect(screen.getByText(/FlareSolverr/i)).toBeInTheDocument();
    expect(screen.queryByText(RAW)).toBeNull();
  });

  it.each(["cloudflare", "solver", "solver_missing"])(
    "does not promise a browser window for %s",
    (code) => {
      render(<TopicError topic={topicWith(RAW, code)} />);
      expect(screen.queryByText(/needs a browser/i)).toBeNull();
    },
  );

  // `client` and `internal` name components that are not the tracker. Their
  // whole reason for existing is that the raw text ("...: context deadline
  // exceeded") reads exactly like a tracker timeout, so a silent fall-through
  // to the raw error would reinstate the misdiagnosis these codes remove.
  it("renders the friendly message for a torrent-client failure", () => {
    render(<TopicError topic={topicWith(RAW, "client")} />);
    expect(screen.getByText(/couldn't reach your torrent client/i)).toBeInTheDocument();
    expect(screen.queryByText(RAW)).toBeNull();
  });

  it("renders the friendly message for an internal failure", () => {
    render(<TopicError topic={topicWith(RAW, "internal")} />);
    expect(screen.getByText(/couldn't save/i)).toBeInTheDocument();
    expect(screen.queryByText(RAW)).toBeNull();
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
