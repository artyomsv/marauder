# Phase 3b — SSE Frontend Consumption Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consume the Phase 3a SSE stream in the browser: one `EventSource` connection routes live events into the React Query cache and a check-status store, the per-topic download progress bar is driven by SSE (the `/status` poll becomes a fallback), and the UI shows live "checking…" + a `next_check_at` countdown.

**Architecture:** An `EventStreamProvider` (mounted inside the authenticated layout) runs a single `useEventStream` hook: it exchanges the JWT for a one-time ticket (`POST /events/ticket`), opens `EventSource('/api/v1/events?ticket=…')`, and on each message calls a pure `applyEvent(queryClient, ev)` router that updates `QK.topicStatus`/invalidates `QK.topics`/updates a `useCheckStatus` zustand store. Because the ticket is single-use, native auto-reconnect can't work — the hook does **manual reconnect** with a fresh ticket and passes the last seen id as `?last_event_id=` (a small backend addition) so replay still works. A `useSseStatus` store exposes the connection state so `DeliveryStatus` can disable its poll while SSE is live.

**Tech Stack:** React 19.2 + Vite + TypeScript, `@tanstack/react-query`, zustand, Vitest + RTL + jsdom (EventSource is mocked — jsdom has none). Plus one small Go backend change (query-param replay fallback).

## Global Constraints

- **Builds on Phase 3a (same branch).** The backend already serves `POST /api/v1/events/ticket` (JWT-authed → `{"ticket": "<hex>"}`) and `GET /api/v1/events?ticket=…` (`text/event-stream`, `Last-Event-ID` replay via header, 25s heartbeat). The wire shape per `data:` line is `WireEvent` = `{id:number, type:string, topic_id?:string, severity?:string, title?:string, body?:string, link?:string, data?:Record<string,unknown>}`. `download.progress`/`download.completed` carry `data: {infohash, percent_done, state}`. `check.completed` carries `data: {next_check_at}` (RFC3339). Heartbeats are `: ping` comments (ignored by `EventSource`).
- **Single-use ticket ⇒ manual reconnect.** Do NOT rely on the browser's native `EventSource` reconnect (it reuses the consumed ticket → 401). On `error`/close, the hook closes the source, fetches a NEW ticket, and reopens with exponential backoff (1s → cap 30s, reset to 1s on `open`).
- **Replay across manual reconnect:** a freshly-constructed `EventSource` does not send the `Last-Event-ID` header, so the hook tracks `lastEventId` (from `MessageEvent.lastEventId`, set by the backend's `id:` line on persisted events) and appends `&last_event_id=<n>` to the URL. **Task 1 adds the backend query-param fallback** so the server reads it when the header is absent.
- **SSE replaces polling.** While SSE is connected, `DeliveryStatus`'s React Query `refetchInterval` returns `false` (updates arrive via `setQueryData`); when disconnected it falls back to the existing adaptive 3s/20s poll. The initial `topicStatus` fetch still runs once on mount (so the cache exists for `setQueryData` to patch).
- **One connection app-wide**, mounted only when authenticated (inside `ProtectedLayout`, which is inside `QueryClientProvider`). Tear down on unmount/logout.
- **Frontend conventions:** `interface` for object shapes, `@/` alias, React Query keys from `QK` only, zustand stores in `lib/`, `useT()` for copy, `lucide-react` icons, components ≤250 lines, co-located `*.test.tsx`. Run:
  `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
  (node_modules is already installed in this worktree's frontend.)
- **Backend (Task 1 only):** `docker run --rm -v "…/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."`.
- **No AI-attribution in commits.** Imperative subject ≤72 chars. Reference `#93`.

---

## File Structure

**Backend (modify):**
- `backend/internal/api/handlers/sse.go` — `Stream` reads `?last_event_id=` as a fallback for the `Last-Event-ID` header.
- `backend/internal/api/handlers/sse_test.go` — a test for the query-param replay path.

**Frontend (create):**
- `frontend/src/lib/sse-status.ts` — `useSseStatus` zustand store (connected boolean).
- `frontend/src/lib/check-status.ts` — `useCheckStatus` zustand store (per-topic check phase + next_check_at).
- `frontend/src/lib/events-stream.ts` — `WireEvent` type + `applyEvent(qc, ev)` router.
- `frontend/src/lib/events-stream.test.ts`
- `frontend/src/lib/hooks/useEventStream.ts` — the connection hook.
- `frontend/src/components/EventStreamProvider.tsx` — mounts the hook.
- `frontend/src/lib/hooks/useEventStream.test.tsx`
- `frontend/src/components/topics/TopicCheckStatus.tsx` — live "checking…" / countdown chip.
- `frontend/src/components/topics/TopicCheckStatus.test.tsx`

**Frontend (modify):**
- `frontend/src/lib/api.ts` — `api.eventsTicket()`.
- `frontend/src/App.tsx` — mount `EventStreamProvider` in `ProtectedLayout`.
- `frontend/src/components/topics/DeliveryStatus.tsx` — disable poll when SSE connected.
- `frontend/src/pages/Topics.tsx` — mount `TopicCheckStatus` in the topic row.
- `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts` — check-status copy.
- `CLAUDE.md`, `CHANGELOG.md` — docs.

---

## Task 1: Backend — `last_event_id` query fallback

**Files:**
- Modify: `backend/internal/api/handlers/sse.go` (the `Stream` replay block)
- Test: `backend/internal/api/handlers/sse_test.go`

**Interfaces:**
- Produces: `Stream` replays from `?last_event_id=` when the `Last-Event-ID` header is empty.

- [ ] **Step 1: Write the failing test** — add to `sse_test.go`. Drive `Stream` with a valid ticket and `?last_event_id=5` (no header); assert `ListForUserSince` is called with `sinceID==5`. Use a recording fake lister:

```go
type recordingLister struct{ since int64 }

func (r *recordingLister) ListForUserSince(_ context.Context, _ uuid.UUID, sinceID int64) ([]*domain.TopicEvent, error) {
	r.since = sinceID
	return nil, nil
}

func TestSSE_Stream_ReplaysFromQueryParam(t *testing.T) {
	lister := &recordingLister{}
	h := &SSE{Hub: &fakeHub{ch: make(chan []byte, 1)}, Tickets: &fakeTickets{issued: "tok", uid: uuid.New(), valid: true}, Events: lister, HeartbeatInterval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?ticket=tok&last_event_id=5", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.Stream(rec, req); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if lister.since != 5 {
		t.Fatalf("ListForUserSince sinceID = %d, want 5 (from query param)", lister.since)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go test ./internal/api/handlers/ -run TestSSE_Stream_ReplaysFromQueryParam"`
Expected: FAIL — `lister.since` is 0 (header empty, query param ignored).

- [ ] **Step 3: Implement** — in `Stream`, change the replay-id source to prefer the header, fall back to the query param:

```go
	// EventSource sets Last-Event-ID on its own auto-reconnect, but the
	// frontend does a manual reconnect (fresh ticket each time) on which the
	// browser can't set the header — so accept ?last_event_id= as a fallback.
	lastIDStr := r.Header.Get("Last-Event-ID")
	if lastIDStr == "" {
		lastIDStr = r.URL.Query().Get("last_event_id")
	}
	if lastID, err := strconv.ParseInt(lastIDStr, 10, 64); err == nil && lastID > 0 {
		if rows, lerr := h.Events.ListForUserSince(r.Context(), uid, lastID); lerr == nil {
			for _, e := range rows {
				if frame := sse.FrameFromTopicEvent(e); frame != nil {
					_, _ = w.Write(frame)
				}
			}
			flusher.Flush()
		}
	}
```

(Replaces the existing `strconv.ParseInt(r.Header.Get("Last-Event-ID"), …)` block.)

- [ ] **Step 4: Run tests + full suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go test -race ./internal/api/handlers/ -run TestSSE"`
Expected: PASS (new test + existing SSE tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/handlers/sse.go backend/internal/api/handlers/sse_test.go
git commit -m "feat: accept last_event_id query param for SSE replay (#93)"
```

---

## Task 2: Frontend — ticket API, WireEvent type, stores

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Create: `frontend/src/lib/sse-status.ts`, `frontend/src/lib/check-status.ts`, `frontend/src/lib/check-status.test.ts`

**Interfaces:**
- Produces:
  - `api.eventsTicket(): Promise<{ ticket: string }>` (POST `/events/ticket`).
  - `useSseStatus` store: `{ connected: boolean; setConnected(c: boolean): void }`.
  - `useCheckStatus` store: `{ byTopic: Record<string, CheckEntry>; setChecking(id): void; setChecked(id, nextCheckAt?): void; setFailed(id, error?): void }` where `CheckEntry = { phase: "checking"|"idle"|"error"; nextCheckAt?: string; error?: string }`.

- [ ] **Step 1: Add `api.eventsTicket`** to `api.ts` (inside the `api` object, near the other POSTs):

```ts
  eventsTicket: () => api.post<{ ticket: string }>("/events/ticket"),
```

- [ ] **Step 2: Create `sse-status.ts`**

```ts
import { create } from "zustand";

// Tracks whether the live SSE connection is up, so views can disable
// polling fallbacks while events are streaming.
interface SseStatusState {
  connected: boolean;
  setConnected: (connected: boolean) => void;
}

export const useSseStatus = create<SseStatusState>((set) => ({
  connected: false,
  setConnected: (connected) => set({ connected }),
}));
```

- [ ] **Step 3: Write the failing test** for the check-status store (`check-status.test.ts`):

```ts
import { describe, it, expect, beforeEach } from "vitest";
import { useCheckStatus } from "@/lib/check-status";

describe("useCheckStatus", () => {
  beforeEach(() => useCheckStatus.setState({ byTopic: {} }));

  it("tracks checking → checked with next_check_at", () => {
    useCheckStatus.getState().setChecking("t1");
    expect(useCheckStatus.getState().byTopic["t1"].phase).toBe("checking");
    useCheckStatus.getState().setChecked("t1", "2026-06-26T10:00:00Z");
    const e = useCheckStatus.getState().byTopic["t1"];
    expect(e.phase).toBe("idle");
    expect(e.nextCheckAt).toBe("2026-06-26T10:00:00Z");
  });

  it("records a failure with its message", () => {
    useCheckStatus.getState().setFailed("t2", "boom");
    const e = useCheckStatus.getState().byTopic["t2"];
    expect(e.phase).toBe("error");
    expect(e.error).toBe("boom");
  });
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- check-status"`
Expected: FAIL — module missing.

- [ ] **Step 5: Create `check-status.ts`**

```ts
import { create } from "zustand";

export type CheckPhase = "checking" | "idle" | "error";

export interface CheckEntry {
  phase: CheckPhase;
  nextCheckAt?: string;
  error?: string;
}

// Live per-topic check status, fed by check.* SSE events. Drives the topic
// row's "checking…" pulse and next-check countdown.
interface CheckStatusState {
  byTopic: Record<string, CheckEntry>;
  setChecking: (topicId: string) => void;
  setChecked: (topicId: string, nextCheckAt?: string) => void;
  setFailed: (topicId: string, error?: string) => void;
}

export const useCheckStatus = create<CheckStatusState>((set) => ({
  byTopic: {},
  setChecking: (topicId) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "checking" } } })),
  setChecked: (topicId, nextCheckAt) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "idle", nextCheckAt } } })),
  setFailed: (topicId, error) =>
    set((s) => ({ byTopic: { ...s.byTopic, [topicId]: { phase: "error", error } } })),
}));
```

- [ ] **Step 6: Run tests + typecheck**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- check-status"`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/lib/sse-status.ts frontend/src/lib/check-status.ts frontend/src/lib/check-status.test.ts
git commit -m "feat: frontend SSE ticket api + status/check stores (#93)"
```

---

## Task 3: Frontend — applyEvent router

**Files:**
- Create: `frontend/src/lib/events-stream.ts`, `frontend/src/lib/events-stream.test.ts`

**Interfaces:**
- Consumes: `QK`, `TopicStatus`/`DeliveryStatus` types from `api.ts`, `useCheckStatus` (Task 2), `QueryClient`.
- Produces: `WireEvent` interface + `applyEvent(qc: QueryClient, ev: WireEvent): void`.

- [ ] **Step 1: Write the failing test** (`events-stream.test.ts`)

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { applyEvent } from "@/lib/events-stream";
import { QK } from "@/lib/queryKeys";
import { useCheckStatus } from "@/lib/check-status";
import type { TopicStatus } from "@/lib/api";

describe("applyEvent", () => {
  beforeEach(() => useCheckStatus.setState({ byTopic: {} }));

  it("patches topicStatus cache on download.progress by infohash", () => {
    const qc = new QueryClient();
    const seed: TopicStatus = {
      client_supports_status: true,
      deliveries: [{ label: "s01e01", infohash: "ABC", delivered_at: "x", state: "downloading", percent_done: 0.1 }],
    };
    qc.setQueryData(QK.topicStatus("t1"), seed);
    applyEvent(qc, {
      id: 0, type: "download.progress", topic_id: "t1",
      data: { infohash: "abc", percent_done: 0.6, state: "downloading" },
    });
    const got = qc.getQueryData<TopicStatus>(QK.topicStatus("t1"));
    expect(got?.deliveries[0].percent_done).toBe(0.6);
  });

  it("updates check store on check.completed with next_check_at", () => {
    const qc = new QueryClient();
    applyEvent(qc, { id: 7, type: "check.completed", topic_id: "t1", data: { next_check_at: "2026-06-26T10:00:00Z" } });
    expect(useCheckStatus.getState().byTopic["t1"].nextCheckAt).toBe("2026-06-26T10:00:00Z");
  });

  it("invalidates topics list on release.found", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 3, type: "release.found", topic_id: "t1" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topics });
  });

  it("refetches topic status on download.completed (no infohash to patch)", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: 9, type: "download.completed", topic_id: "t1" });
    expect(spy).toHaveBeenCalledWith({ queryKey: QK.topicStatus("t1") });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- events-stream"`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement** `events-stream.ts`

```ts
import type { QueryClient } from "@tanstack/react-query";

import { QK } from "@/lib/queryKeys";
import { useCheckStatus } from "@/lib/check-status";
import type { TopicStatus } from "@/lib/api";

// WireEvent mirrors the backend sse.wireEvent JSON in each `data:` line.
export interface WireEvent {
  id: number;
  type: string;
  topic_id?: string;
  severity?: string;
  title?: string;
  body?: string;
  link?: string;
  data?: Record<string, unknown>;
}

// applyEvent routes one live event into the React Query cache / check store.
// Pure w.r.t. its inputs (no network); safe to unit-test.
export function applyEvent(qc: QueryClient, ev: WireEvent): void {
  const topicId = ev.topic_id;
  switch (ev.type) {
    case "download.progress": {
      // Progress events carry {infohash, percent_done, state} — patch in place.
      if (topicId) patchDeliveryProgress(qc, topicId, ev);
      return;
    }
    case "download.completed": {
      // The completed event carries no infohash in Data (only title/body), so
      // there's nothing to patch by hash — refetch the topic's status to pick
      // up the finished delivery.
      if (topicId) qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
      return;
    }
    case "check.started":
      if (topicId) useCheckStatus.getState().setChecking(topicId);
      return;
    case "check.completed":
      if (topicId) useCheckStatus.getState().setChecked(topicId, ev.data?.next_check_at as string | undefined);
      return;
    case "check.failed":
      if (topicId) useCheckStatus.getState().setFailed(topicId, ev.body);
      return;
    case "release.found":
    case "download.submitted":
      if (topicId) qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
      qc.invalidateQueries({ queryKey: QK.topics });
      return;
    case "topic.added":
      qc.invalidateQueries({ queryKey: QK.topics });
      return;
    default:
      return;
  }
}

function patchDeliveryProgress(qc: QueryClient, topicId: string, ev: WireEvent): void {
  const infohash = (ev.data?.infohash as string | undefined)?.toLowerCase();
  if (!infohash) return;
  const cached = qc.getQueryData<TopicStatus>(QK.topicStatus(topicId));
  if (!cached) {
    // No cached deliveries yet (e.g. a brand-new one) — refetch to pick it up.
    qc.invalidateQueries({ queryKey: QK.topicStatus(topicId) });
    return;
  }
  const percent = ev.data?.percent_done as number | undefined;
  const state = ev.data?.state as string | undefined;
  qc.setQueryData<TopicStatus>(QK.topicStatus(topicId), {
    ...cached,
    deliveries: cached.deliveries.map((d) =>
      d.infohash.toLowerCase() === infohash
        ? { ...d, percent_done: percent ?? d.percent_done, state: state ?? d.state }
        : d,
    ),
  });
}
```

- [ ] **Step 4: Run tests + typecheck**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- events-stream"`
Expected: PASS.

> Implementer note: confirm the `TopicStatus` / `DeliveryStatus` field names in `api.ts` match what `patchDeliveryProgress` reads (`deliveries[].infohash`, `.percent_done`, `.state`). Adjust the patch if the real shape differs; the cache type is the source of truth.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/events-stream.ts frontend/src/lib/events-stream.test.ts
git commit -m "feat: route live SSE events into the query cache (#93)"
```

---

## Task 4: Frontend — useEventStream hook + provider + mount

**Files:**
- Create: `frontend/src/lib/hooks/useEventStream.ts`, `frontend/src/components/EventStreamProvider.tsx`, `frontend/src/lib/hooks/useEventStream.test.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Consumes: `api.eventsTicket`, `API_BASE`, `applyEvent`, `useSseStatus`, `useQueryClient`.
- Produces: `useEventStream()` (hook, no return); `<EventStreamProvider>{children}</EventStreamProvider>`.

- [ ] **Step 1: Write the failing test** (`useEventStream.test.tsx`) — mock `EventSource` and `api`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { EventStreamProvider } from "@/components/EventStreamProvider";
import { useSseStatus } from "@/lib/sse-status";

vi.mock("@/lib/api", () => ({
  API_BASE: "/api/v1",
  api: { eventsTicket: vi.fn().mockResolvedValue({ ticket: "tok123" }) },
}));

class MockEventSource {
  static last: MockEventSource | null = null;
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(url: string) {
    this.url = url;
    MockEventSource.last = this;
    queueMicrotask(() => this.onopen?.());
  }
  emit(data: string, lastEventId = "") {
    this.onmessage?.({ data, lastEventId } as MessageEvent);
  }
}

beforeEach(() => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource);
  useSseStatus.setState({ connected: false });
  MockEventSource.last = null;
});

function wrap() {
  const qc = new QueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <EventStreamProvider>
        <div>child</div>
      </EventStreamProvider>
    </QueryClientProvider>,
  );
}

describe("EventStreamProvider", () => {
  it("fetches a ticket, opens EventSource with it, and marks connected", async () => {
    wrap();
    await waitFor(() => expect(MockEventSource.last).not.toBeNull());
    expect(MockEventSource.last!.url).toContain("/api/v1/events?ticket=tok123");
    await waitFor(() => expect(useSseStatus.getState().connected).toBe(true));
  });

  it("renders its children", async () => {
    const { getByText } = wrap();
    expect(getByText("child")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- useEventStream"`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement** `useEventStream.ts`

```ts
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api, API_BASE } from "@/lib/api";
import { applyEvent, type WireEvent } from "@/lib/events-stream";
import { useSseStatus } from "@/lib/sse-status";

const BACKOFF_START = 1000;
const BACKOFF_MAX = 30000;

// useEventStream maintains a single live SSE connection while mounted. The
// SSE ticket is single-use, so native auto-reconnect can't work — we manage
// reconnection manually (fresh ticket each attempt), and carry the last seen
// event id forward as a query param so replay survives a reconnect.
export function useEventStream(): void {
  const qc = useQueryClient();
  useEffect(() => {
    let stopped = false;
    let es: EventSource | null = null;
    let backoff = BACKOFF_START;
    let lastEventId = "";
    let timer: ReturnType<typeof setTimeout> | undefined;

    const scheduleReconnect = () => {
      if (stopped) return;
      backoff = Math.min(backoff * 2, BACKOFF_MAX);
      timer = setTimeout(connect, backoff);
    };

    async function connect() {
      if (stopped) return;
      let ticket: string;
      try {
        ticket = (await api.eventsTicket()).ticket;
      } catch {
        scheduleReconnect();
        return;
      }
      if (stopped) return;
      let url = `${API_BASE}/events?ticket=${encodeURIComponent(ticket)}`;
      if (lastEventId) url += `&last_event_id=${encodeURIComponent(lastEventId)}`;
      es = new EventSource(url);
      es.onopen = () => {
        backoff = BACKOFF_START;
        useSseStatus.getState().setConnected(true);
      };
      es.onmessage = (e) => {
        if (e.lastEventId) lastEventId = e.lastEventId;
        try {
          applyEvent(qc, JSON.parse(e.data) as WireEvent);
        } catch {
          // ignore malformed frame
        }
      };
      es.onerror = () => {
        useSseStatus.getState().setConnected(false);
        es?.close();
        es = null;
        scheduleReconnect();
      };
    }

    connect();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      es?.close();
      useSseStatus.getState().setConnected(false);
    };
  }, [qc]);
}
```

`EventStreamProvider.tsx`:

```tsx
import type { ReactNode } from "react";
import { useEventStream } from "@/lib/hooks/useEventStream";

// EventStreamProvider runs the single app-wide SSE connection for as long as
// it's mounted (inside the authenticated layout). It renders its children
// unchanged — it's a lifecycle host, not a context provider.
export function EventStreamProvider({ children }: { children: ReactNode }) {
  useEventStream();
  return <>{children}</>;
}
```

- [ ] **Step 4: Mount in `App.tsx`** — wrap the authenticated `Outlet` so the connection runs only when logged in and inside React Query. In `ProtectedLayout`, replace `return <Outlet />;` with:

```tsx
  return (
    <EventStreamProvider>
      <Outlet />
    </EventStreamProvider>
  );
```

Add the import `import { EventStreamProvider } from "@/components/EventStreamProvider";`.

- [ ] **Step 5: Run tests + typecheck + build**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- useEventStream && npm run build"`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/lib/hooks/useEventStream.ts frontend/src/components/EventStreamProvider.tsx frontend/src/lib/hooks/useEventStream.test.tsx frontend/src/App.tsx
git commit -m "feat: live SSE connection provider with manual reconnect (#93)"
```

---

## Task 5: Frontend — retire the /status poll to a fallback

**Files:**
- Modify: `frontend/src/components/topics/DeliveryStatus.tsx`
- Test: `frontend/src/components/topics/DeliveryStatus.test.tsx` (create if absent)

**Interfaces:**
- Consumes: `useSseStatus`.

Extract the interval decision into a small exported pure helper so it's unit-testable without poking React Query internals.

- [ ] **Step 1: Write the failing test** (`DeliveryStatus.test.tsx`) — test the helper directly:

```tsx
import { describe, it, expect } from "vitest";
import { pollInterval, ACTIVE_POLL_MS, IDLE_POLL_MS } from "@/components/topics/DeliveryStatus";

describe("pollInterval", () => {
  const downloading = [{ state: "downloading" }];
  const idle = [{ state: "seeding" }];

  it("disables polling while SSE is connected", () => {
    expect(pollInterval(true, downloading)).toBe(false);
    expect(pollInterval(true, idle)).toBe(false);
  });

  it("polls fast when disconnected and a delivery is active", () => {
    expect(pollInterval(false, downloading)).toBe(ACTIVE_POLL_MS);
  });

  it("polls slow when disconnected and nothing is active", () => {
    expect(pollInterval(false, idle)).toBe(IDLE_POLL_MS);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- DeliveryStatus"`
Expected: FAIL — `pollInterval` not exported.

- [ ] **Step 3: Implement** — in `DeliveryStatus.tsx`, export `ACTIVE_POLL_MS`/`IDLE_POLL_MS` (change `const ACTIVE_POLL_MS` → `export const ACTIVE_POLL_MS`, same for IDLE), add the helper, read the store, and use the helper in the query:

```ts
// pollInterval decides the /status refetch cadence. While SSE is connected,
// live updates arrive via setQueryData, so polling is disabled; otherwise it
// falls back to fast-while-active / slow-baseline.
export function pollInterval(
  sseConnected: boolean,
  deliveries: { state: string }[] | undefined,
): number | false {
  if (sseConnected) return false;
  const active = deliveries?.some((x) => ACTIVE_STATES.has(x.state));
  return active ? ACTIVE_POLL_MS : IDLE_POLL_MS;
}
```

In the component, read the store and call the helper:

```ts
  const sseConnected = useSseStatus((s) => s.connected);
  // ...
    refetchInterval: (query) => pollInterval(sseConnected, query.state.data?.deliveries),
```

Add the import `import { useSseStatus } from "@/lib/sse-status";`.

- [ ] **Step 4: Run tests + typecheck**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test -- DeliveryStatus"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/topics/DeliveryStatus.tsx frontend/src/components/topics/DeliveryStatus.test.tsx
git commit -m "feat: drive delivery progress from SSE, poll as fallback (#93)"
```

---

## Task 6: Frontend — live check-status chip in the topic row

**Files:**
- Create: `frontend/src/components/topics/TopicCheckStatus.tsx`, `frontend/src/components/topics/TopicCheckStatus.test.tsx`
- Modify: `frontend/src/pages/Topics.tsx`, `frontend/src/i18n/en.ts`, `frontend/src/i18n/ru.ts`

**Interfaces:**
- Consumes: `useCheckStatus`, `useT`.
- Produces: `<TopicCheckStatus topicId={string} />`.

- [ ] **Step 1: Write the failing test** (`TopicCheckStatus.test.tsx`)

```tsx
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm test -- TopicCheckStatus"`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement** `TopicCheckStatus.tsx`

```tsx
import { Loader2, AlertCircle, Clock } from "lucide-react";

import { useCheckStatus } from "@/lib/check-status";
import { useT } from "@/i18n";

interface TopicCheckStatusProps {
  topicId: string;
}

// TopicCheckStatus shows the live check state for a topic, fed by check.*
// SSE events: a "checking…" pulse, an error chip, or a next-check countdown.
// Renders nothing until an event has arrived for this topic.
export function TopicCheckStatus({ topicId }: TopicCheckStatusProps) {
  const t = useT();
  const entry = useCheckStatus((s) => s.byTopic[topicId]);
  if (!entry) return null;

  if (entry.phase === "checking") {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-primary" title={t("topics.check.checking")}>
        <Loader2 className="size-3 animate-spin" />
        {t("topics.check.checking")}
      </span>
    );
  }
  if (entry.phase === "error") {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-destructive" title={entry.error ?? t("topics.check.error")}>
        <AlertCircle className="size-3" />
        {t("topics.check.error")}
      </span>
    );
  }
  if (entry.nextCheckAt) {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground" title={new Date(entry.nextCheckAt).toLocaleString()}>
        <Clock className="size-3" />
        {t("topics.check.next", { time: new Date(entry.nextCheckAt).toLocaleTimeString() })}
      </span>
    );
  }
  return null;
}
```

- [ ] **Step 4: Add i18n keys** to `en.ts` (and Russian to `ru.ts`):

```ts
  "topics.check.checking": "Checking…",
  "topics.check.error": "Check error",
  "topics.check.next": "Next check {time}",
```

- [ ] **Step 5: Mount in the topic row** — in `Topics.tsx`, render `<TopicCheckStatus topicId={topic.id} />` in the topic row's status area (near where the topic's status/last-checked is shown). Keep the addition to the mount line + import only (Topics.tsx is over budget; do not add logic there). Import `TopicCheckStatus`.

- [ ] **Step 6: Run full frontend suite**

Run: `docker run --rm -v "E:/Projects/Stukans/Marauder/.worktrees/93-event-type-taxonomy-per-event-notifier-subscriptions-and-live-sse-updates/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/topics/TopicCheckStatus.tsx frontend/src/components/topics/TopicCheckStatus.test.tsx frontend/src/pages/Topics.tsx frontend/src/i18n/en.ts frontend/src/i18n/ru.ts
git commit -m "feat: live check-status chip in the topic row (#93)"
```

---

## Task 7: Docs

**Files:**
- Modify: `CLAUDE.md`, `CHANGELOG.md`

- [ ] **Step 1: `CLAUDE.md`** — in the Frontend section, note the SSE consumption layer: `lib/hooks/useEventStream` (single live connection, manual reconnect with fresh ticket + `last_event_id`), `components/EventStreamProvider` (mounted in `ProtectedLayout`), `lib/events-stream` (`applyEvent` cache router), `lib/sse-status` + `lib/check-status` stores, `components/topics/TopicCheckStatus`. Note that `DeliveryStatus` now disables its poll while SSE is connected (poll is the fallback). Mention the backend `?last_event_id=` query fallback on `GET /events`.

- [ ] **Step 2: `CHANGELOG.md`** — add under `[Unreleased]` → `### Added`:

```markdown
- The web UI now consumes the live event stream: download progress, "checking…"/next-check status, and new-release/finished updates arrive over SSE in real time (the status poll is now only a fallback) (#93).
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md CHANGELOG.md
git commit -m "docs: document the SSE frontend consumption layer (#93)"
```

---

## Final verification

- [ ] **Backend:** `docker run --rm -v "…/backend:/backend" -w //backend golang:1.25 sh -c "go build ./... && go vet ./... && go test -race ./..."` → pass.
- [ ] **Frontend:** `docker run --rm -v "…/frontend:/frontend" -w //frontend node:20-alpine sh -c "npm run typecheck && npm test && npm run build"` → pass.
- [ ] **Manual smoke (dev stack):** log in; trigger a check; confirm the topic row shows "Checking…" then a next-check time, a download shows a live-moving progress bar with no network polling of `/status` (watch the Network tab — the EventSource stays open, `/status` stops polling), and a finished download flips live.

---

## Spec coverage (Phase 3 frontend portion of `2026-06-25-event-types-and-live-updates-design.md` §7)

- §7 `useEventStream` provider (one connection; ticket; reconnect→refetch ticket) → Task 4. Route events into the React Query cache → Task 3. Disable poll while SSE connected, keep as fallback → Task 5. Live check status (`next_check_at` countdown / "checking…" pulse) → Task 6. The single-use-ticket manual-reconnect + `last_event_id` query fallback (a refinement discovered grounding the real `EventSource` API) → Tasks 1, 4. This completes the §6.6/§7 transport: the SSE backend (Phase 3a) plus this consumption layer.
