import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { TopicRow, type TopicRowActions, type TopicRowLookups } from "./TopicRow";
import type { Topic } from "@/lib/api";

const lookups: TopicRowLookups = {
  clientById: new Map(),
  defaultClient: null,
  notifierById: new Map(),
};

function makeTopic(status: Topic["Status"]): Topic {
  return {
    ID: "t1",
    DisplayName: "Some Show",
    URL: "https://rutracker.org/forum/viewtopic.php?t=1",
    TrackerName: "rutracker",
    Status: status,
  } as unknown as Topic;
}

function makeActions(): TopicRowActions {
  return {
    onToggleSelect: vi.fn(),
    onEdit: vi.fn(),
    onRecheck: vi.fn(),
    onReset: vi.fn(),
    onDelete: vi.fn(),
  };
}

function renderRow(status: Topic["Status"], actions: TopicRowActions) {
  // TopicRow renders DeliveryStatus / TopicCheckStatus / TopicHistoryDisclosure,
  // which call useQuery — a QueryClientProvider is required or the render throws.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TopicRow
        topic={makeTopic(status)}
        compact={false}
        selected={false}
        deletePending={false}
        lookups={lookups}
        actions={actions}
      />
    </QueryClientProvider>,
  );
}

describe("TopicRow check-now action", () => {
  it("calls onRecheck when clicked", async () => {
    const actions = makeActions();
    renderRow("error", actions);

    await userEvent.click(screen.getByRole("button", { name: /check now/i }));
    expect(actions.onRecheck).toHaveBeenCalledTimes(1);
  });

  it("is offered for an errored topic — the case the feature exists for", () => {
    renderRow("error", makeActions());
    expect(screen.getByRole("button", { name: /check now/i })).toBeInTheDocument();
  });

  // The scheduler skips paused topics entirely, so a Check now button on one
  // would silently do nothing. Hiding it is the honest option.
  it("is hidden for a paused topic", () => {
    renderRow("paused", makeActions());
    expect(screen.queryByRole("button", { name: /check now/i })).not.toBeInTheDocument();
  });
});
