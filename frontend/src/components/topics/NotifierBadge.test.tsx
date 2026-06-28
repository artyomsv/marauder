import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { NotifierBadge, type NotifierRef } from "./NotifierBadge";
import type { Topic } from "@/lib/api";

function topic(overrides: Partial<Topic>): Topic {
  return {
    ID: "t1", UserID: "u1", TrackerName: "rutracker", URL: "x",
    DisplayName: "Show", ImageURL: "", ClientID: null, NotifierID: null,
    DownloadDir: "", Category: "", ReplaceOnUpdate: false, ReplaceDeleteData: true, Extra: null, LastHash: "",
    LastCheckedAt: null, LastUpdatedAt: null, NextCheckAt: "", CheckIntervalSec: 900,
    ConsecutiveErrors: 0, Status: "active", LastError: "", LastErrorCode: "", CreatedAt: "", UpdatedAt: "",
    ...overrides,
  };
}

const byId = new Map<string, NotifierRef>([["n1", { id: "n1", display_name: "Main Telegram" }]]);

describe("NotifierBadge", () => {
  it("renders the notifier name when an override is set", () => {
    render(<NotifierBadge topic={topic({ NotifierID: "n1" })} notifierById={byId} />);
    expect(screen.getByText("Main Telegram")).toBeInTheDocument();
  });

  it("renders the default-notifiers fallback when no override is set", () => {
    render(<NotifierBadge topic={topic({ NotifierID: null })} notifierById={byId} />);
    expect(screen.getByText("default notifiers")).toBeInTheDocument();
  });

  it("renders 'unknown notifier' when the assigned notifier is missing", () => {
    render(<NotifierBadge topic={topic({ NotifierID: "gone" })} notifierById={byId} />);
    expect(screen.getByText("unknown notifier")).toBeInTheDocument();
  });
});
