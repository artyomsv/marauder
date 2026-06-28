import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { ClientBadge, type ClientRef } from "./ClientBadge";
import type { Topic } from "@/lib/api";

const QBIT: ClientRef = { id: "c1", display_name: "qBittorrent", is_default: true };
const TRANSMISSION: ClientRef = { id: "c2", display_name: "Transmission", is_default: false };

const clientById = new Map([
  [QBIT.id, QBIT],
  [TRANSMISSION.id, TRANSMISSION],
]);

// Minimal topic; only ClientID is read by the badge.
function topicWith(clientID: string | null): Topic {
  return {
    ID: "t-1",
    UserID: "u-1",
    TrackerName: "lostfilm",
    URL: "https://example.test",
    DisplayName: "Some Show",
    ImageURL: "",
    ClientID: clientID,
    NotifierID: null,
    DownloadDir: "",
    Category: "",
    ReplaceOnUpdate: false,
    ReplaceDeleteData: true,
    Extra: null,
    LastHash: "",
    LastCheckedAt: null,
    LastUpdatedAt: null,
    NextCheckAt: "",
    CheckIntervalSec: 900,
    ConsecutiveErrors: 0,
    Status: "active",
    LastError: "",
    LastErrorCode: "",
    CreatedAt: "",
    UpdatedAt: "",
  };
}

describe("ClientBadge", () => {
  it("shows the explicitly selected client's name", () => {
    render(
      <ClientBadge topic={topicWith("c2")} clientById={clientById} defaultClient={QBIT} />,
    );
    expect(screen.getByText("Transmission")).toBeInTheDocument();
  });

  it("falls back to the default client when the topic has no client", () => {
    render(
      <ClientBadge topic={topicWith(null)} clientById={clientById} defaultClient={QBIT} />,
    );
    expect(screen.getByText("qBittorrent · default")).toBeInTheDocument();
  });

  it("warns when no client is set and there is no default", () => {
    render(
      <ClientBadge topic={topicWith(null)} clientById={clientById} defaultClient={null} />,
    );
    expect(screen.getByText("no default client")).toBeInTheDocument();
  });

  it("flags an assigned client that no longer exists", () => {
    render(
      <ClientBadge topic={topicWith("gone")} clientById={clientById} defaultClient={QBIT} />,
    );
    expect(screen.getByText("unknown client")).toBeInTheDocument();
  });
});
