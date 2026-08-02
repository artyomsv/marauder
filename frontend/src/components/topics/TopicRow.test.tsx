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

// The row's actions live behind an overflow menu, so every assertion about
// them has to open it first.
async function openActionsMenu() {
  await userEvent.click(screen.getByRole("button", { name: /topic actions/i }));
}

describe("TopicRow check-now action", () => {
  it("calls onRecheck when chosen", async () => {
    const actions = makeActions();
    renderRow("error", actions);

    await openActionsMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: /check now/i }));
    expect(actions.onRecheck).toHaveBeenCalledTimes(1);
  });

  it("is offered for an errored topic — the case the feature exists for", async () => {
    renderRow("error", makeActions());
    await openActionsMenu();
    expect(await screen.findByRole("menuitem", { name: /check now/i })).toBeInTheDocument();
  });

  // The scheduler skips paused topics entirely, so a Check now entry on one
  // would silently do nothing. Omitting it is the honest option.
  it("is hidden for a paused topic", async () => {
    renderRow("paused", makeActions());
    await openActionsMenu();
    // The menu is open — Reset proves it — but Check now is absent.
    expect(await screen.findByRole("menuitem", { name: /reset/i })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /check now/i })).not.toBeInTheDocument();
  });

  // Delete stays two-step inside the menu: a written label removes the
  // ambiguity of an icon, but it does not make the action less destructive.
  it("requires a second confirm before deleting", async () => {
    const actions = makeActions();
    renderRow("active", actions);

    await openActionsMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: /^delete$/i }));
    expect(actions.onDelete).not.toHaveBeenCalled();

    await userEvent.click(await screen.findByRole("menuitem", { name: /confirm delete/i }));
    expect(actions.onDelete).toHaveBeenCalledTimes(1);
  });
});
