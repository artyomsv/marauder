/**
 * Thin fetch wrapper for the Marauder API.
 *
 * - Attaches the access token from the auth store.
 * - Translates RFC 7807 problem responses into Error throws with a
 *   machine-readable `.problem` property.
 * - Base URL defaults to `/api/v1` so the Vite dev proxy can forward
 *   traffic to the backend, and nginx can do the same in production.
 */

import { useAuthStore } from "@/lib/auth-store";

export const API_BASE = "/api/v1";

export type Problem = {
  type?: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  trace_id?: string;
};

export class ApiError extends Error {
  readonly problem: Problem;
  constructor(problem: Problem) {
    super(problem.title + (problem.detail ? `: ${problem.detail}` : ""));
    this.problem = problem;
  }
}

// Singleton in-flight refresh promise. When a 401 races with itself
// across multiple parallel requests (e.g. dashboard opens 5 queries
// simultaneously, all 5 see expired access tokens), only ONE refresh
// call should hit the server — the others wait on this promise.
let refreshInFlight: Promise<boolean> | null = null;

// Path prefixes that must NEVER trigger the 401 → refresh → retry
// dance. /auth/refresh would loop on itself, /auth/login should bubble
// the credentials error up, /auth/logout always 200s anyway.
const NO_REFRESH_PATHS = ["/auth/refresh", "/auth/login", "/auth/logout"];

async function tryRefresh(): Promise<boolean> {
  if (refreshInFlight) return refreshInFlight;

  const refreshToken = useAuthStore.getState().refreshToken;
  if (!refreshToken) return false;

  refreshInFlight = (async () => {
    try {
      const resp = await fetch(API_BASE + "/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (!resp.ok) return false;
      const tokens = (await resp.json()) as TokenPair;
      useAuthStore.getState().setTokens(tokens);
      return true;
    } catch {
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();

  return refreshInFlight;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: { auth?: boolean; _retried?: boolean } = { auth: true },
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (opts.auth !== false) {
    const token = useAuthStore.getState().accessToken;
    if (token) headers.Authorization = `Bearer ${token}`;
  }
  const resp = await fetch(API_BASE + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  // 401 on an authed call → try to refresh the access token once,
  // then replay the original request. If refresh fails (or we're
  // already in a retry loop), clear the auth store so the route
  // guard navigates to /login on the next render.
  const isAuthed = opts.auth !== false;
  const shouldHandle401 =
    resp.status === 401 &&
    isAuthed &&
    !opts._retried &&
    !NO_REFRESH_PATHS.some((p) => path.startsWith(p));

  if (shouldHandle401) {
    const refreshed = await tryRefresh();
    if (refreshed) {
      return request<T>(method, path, body, { ...opts, _retried: true });
    }
    useAuthStore.getState().logout();
    // Fall through and surface the original 401 to the caller — the
    // route guard reacts to the cleared store and navigates away.
  }

  if (resp.status === 204) return undefined as T;

  const text = await resp.text();
  const data = text ? JSON.parse(text) : null;

  if (!resp.ok) {
    const problem: Problem = data ?? {
      title: resp.statusText,
      status: resp.status,
    };
    throw new ApiError(problem);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string, opts?: { auth?: boolean }) =>
    request<T>("GET", path, undefined, opts),
  post: <T>(path: string, body?: unknown, opts?: { auth?: boolean }) =>
    request<T>("POST", path, body, opts),
  put: <T>(path: string, body?: unknown, opts?: { auth?: boolean }) =>
    request<T>("PUT", path, body, opts),
  patch: <T>(path: string, body?: unknown, opts?: { auth?: boolean }) =>
    request<T>("PATCH", path, body, opts),
  del: <T>(path: string, opts?: { auth?: boolean }) =>
    request<T>("DELETE", path, undefined, opts),

  // --- Interactive (captcha) login for trackers that advertise
  // `supports_interactive_login` via /trackers/match. The captcha image
  // is returned as a data URL, so the browser never talks to the tracker
  // directly — Marauder's backend proxies the whole challenge.
  interactiveBegin: (body: { tracker_name: string; username: string; password: string }) =>
    request<InteractiveBeginResult>("POST", "/credentials/interactive/begin", body),
  interactiveComplete: (body: { tracker_name: string; challenge_id: string; answer: string }) =>
    request<{ credential: CredentialView }>("POST", "/credentials/interactive/complete", body),
  interactiveRefresh: (body: { tracker_name: string; challenge_id: string }) =>
    request<InteractiveRefreshResult>("POST", "/credentials/interactive/refresh", body),

  // --- Re-authentication for an existing credential whose session cookie
  // expired. Mirrors the interactive begin/complete pair but uses the
  // stored password server-side, so the body is empty — the user only
  // ever solves a captcha. Image refreshes reuse `interactiveRefresh`.
  reauthBegin: (id: string) =>
    request<InteractiveBeginResult>("POST", `/credentials/${id}/reauth/begin`, {}),
  reauthComplete: (id: string, body: { challenge_id: string; answer: string }) =>
    request<{ credential: CredentialView }>("POST", `/credentials/${id}/reauth/complete`, body),

  // Update an existing topic. URL/tracker are immutable; the backend merges
  // the capability fields into the topic's Extra blob, preserving
  // downloaded_episodes. 404 unknown/foreign, 422 bad quality.
  updateTopic: (id: string, body: UpdateTopicBody) =>
    request<Topic>("PUT", `/topics/${id}`, body),

  // Resolve a real title + poster image from a tracker URL for the AddTopic
  // form preview. Returns empty fields (never an error) when the tracker
  // can't resolve metadata, so callers can render unconditionally.
  previewTracker: (url: string) =>
    request<TrackerPreview>("GET", `/trackers/preview?url=${encodeURIComponent(url)}`),

  // List what a topic has delivered to its client, with live download
  // status when the client supports it (qBittorrent, Transmission). Safe to
  // poll: it degrades to "delivered" labels for clients without status.
  topicStatus: (id: string) =>
    request<TopicStatus>("GET", `/topics/${id}/status`),

  // /topics/{id}/events — read-only per-topic history timeline.
  topicEvents: (id: string) =>
    request<{ events: TopicEvent[] }>("GET", `/topics/${id}/events`),

  // List the categories a configured client already knows about (qBittorrent),
  // so the AddTopic form can suggest them. `supported` is false for clients
  // without a category concept; on any fetch failure the list is empty and the
  // field stays free-text.
  getClientCategories: (id: string) =>
    request<{ supported: boolean; categories: string[] }>(
      "GET",
      `/clients/${id}/categories`,
    ),

  // GET /notifiers/{id} — includes decrypted config for the edit form.
  getNotifier: (id: string) =>
    request<NotifierDetail>("GET", `/notifiers/${id}`),

  // PUT /notifiers/{id} — update display name, events, default flag, and config.
  updateNotifier: (id: string, body: UpdateNotifierBody) =>
    request<{ id: string }>("PUT", `/notifiers/${id}`, body),

  // --- Sonarr integration (admin only). Multiple instances, each with its
  // own enable flag and history cursor. The API key is never returned; the
  // view exposes only `api_key_set`. An empty api_key on save keeps the
  // stored key.
  listSonarrInstances: () =>
    request<SonarrInstance[]>("GET", "/system/sonarr/instances"),
  createSonarrInstance: (body: SonarrInstanceBody) =>
    request<SonarrInstance>("POST", "/system/sonarr/instances", body),
  updateSonarrInstance: (id: string, body: SonarrInstanceBody) =>
    request<SonarrInstance>("PUT", `/system/sonarr/instances/${id}`, body),
  deleteSonarrInstance: (id: string) =>
    request<void>("DELETE", `/system/sonarr/instances/${id}`),
  // Connectivity check against an existing instance's stored url + key.
  testSonarrInstance: (id: string) =>
    request<SonarrTestResult>("POST", `/system/sonarr/instances/${id}/test`, {}),
  // Ad-hoc connectivity check for the add form (before the instance exists).
  testSonarr: (body: { sonarr_url: string; api_key: string }) =>
    request<SonarrTestResult>("POST", "/system/sonarr/test", body),

  // --- Server-Sent Events. POST /events/ticket exchanges the access token
  // for a single-use ticket; GET /events?ticket=… streams event-stream.
  eventsTicket: () => request<{ ticket: string }>("POST", "/events/ticket"),
};

// One configured Sonarr instance. The API key is intentionally absent
// (api_key_set only).
export interface SonarrInstance {
  id: string;
  name: string;
  enabled: boolean;
  sonarr_url: string;
  api_key_set: boolean;
  poll_interval_sec: number;
  allowed_trackers: string[];
  default_client_id: string | null;
  default_category: string;
  default_download_dir: string;
  update_existing: boolean;
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

// Body of POST/PUT /system/sonarr/instances. An empty api_key keeps the stored
// one on update (and means "no key" on create).
export interface SonarrInstanceBody {
  name: string;
  enabled: boolean;
  sonarr_url: string;
  api_key: string;
  poll_interval_sec: number;
  allowed_trackers: string[];
  default_client_id: string | null;
  default_category: string;
  default_download_dir: string;
  update_existing: boolean;
}

export interface SonarrTestResult {
  ok: boolean;
  version: string;
  app_name: string;
}

// One delivered torrent in GET /topics/{id}/status. state is the normalised
// client lifecycle word ("downloading", "seeding", …) or "delivered" when
// no live status is available. percent_done is null unless the client
// reported a live figure (0..1).
export interface DeliveryStatus {
  label: string;
  infohash: string;
  delivered_at: string;
  state: string;
  percent_done: number | null;
}

// Response of GET /topics/{id}/status.
export interface TopicStatus {
  client_supports_status: boolean;
  deliveries: DeliveryStatus[];
}

// One event in the per-topic history timeline (GET /topics/{id}/events).
export interface TopicEvent {
  id: number;
  event_type: string;
  severity: "info" | "warn" | "error";
  message: string;
  data?: Record<string, unknown>;
  created_at: string;
}

// Response of GET /trackers/preview. Either field may be empty.
export interface TrackerPreview {
  title: string;
  image_url: string;
}

// Body of PUT /topics/{id}. Mirrors the backend updateTopicReq.
export interface UpdateTopicBody {
  display_name: string;
  client_id?: string | null;
  notifier_id?: string | null;
  download_dir?: string;
  category?: string;
  // Replace-on-update policy (issue #101). replace_delete_data only matters
  // when replace_on_update is true.
  replace_on_update?: boolean;
  replace_delete_data?: boolean;
  quality?: string;
  start_season?: number;
  start_episode?: number;
}

// GET /notifiers/{id} — includes decrypted config for the edit form.
export interface NotifierDetail {
  id: string;
  notifier_name: string;
  display_name: string;
  events: string[];
  is_default: boolean;
  config: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

// PUT /notifiers/{id} body.
export interface UpdateNotifierBody {
  display_name: string;
  events: string[];
  is_default: boolean;
  config: Record<string, string>;
}

// --- Typed models mirroring backend/internal/domain ---------------------

// One season of a tracker's released-episode catalog, returned by
// GET /trackers/seasons. Episodes are the released episode numbers for
// that season (sorted ascending by the backend).
export interface SeasonInfo {
  number: number;
  episodes: number[];
}

export interface CredentialView {
  id: string;
  tracker_name: string;
  display_name: string;
  username: string;
  session_expired: boolean;
  created_at: string;
  updated_at: string;
}

// Result of POST /credentials/interactive/begin. Either the tracker
// logged us straight in (no captcha required) or it handed back a
// captcha challenge to relay to the user.
export interface InteractiveBeginResult {
  status: "logged_in" | "captcha";
  challenge_id?: string;
  captcha_image?: string;
  credential?: CredentialView;
}

// Result of POST /credentials/interactive/refresh — a fresh captcha
// image bound to the same (or a rotated) challenge id.
export interface InteractiveRefreshResult {
  challenge_id: string;
  captcha_image: string;
}

export type Me = {
  id: string;
  username: string;
  email: string;
  role: "admin" | "user";
};

export type TokenPair = {
  access_token: string;
  access_token_expires_at: string;
  refresh_token: string;
  refresh_token_expires_at: string;
  token_type: string;
};

// The capability fields a tracker plugin reads from a topic's Extra blob.
// Stored as lowercase keys in the JSONB map (see backend topics handler),
// so the JSON shape here is snake_case even though the surrounding Topic
// fields are PascalCase (domain.Topic has no JSON tags).
export interface TopicExtra {
  quality?: string;
  start_season?: number;
  start_episode?: number;
  // How the topic was created. "sonarr" marks topics auto-imported by the
  // Sonarr integration; absent for manually-added topics.
  source?: string;
  [key: string]: unknown;
}

export type Topic = {
  ID: string;
  UserID: string;
  TrackerName: string;
  URL: string;
  DisplayName: string;
  ImageURL: string;
  ClientID: string | null;
  NotifierID: string | null;
  DownloadDir: string;
  Category: string;
  ReplaceOnUpdate: boolean;
  ReplaceDeleteData: boolean;
  Extra: TopicExtra | null;
  LastHash: string;
  LastCheckedAt: string | null;
  LastUpdatedAt: string | null;
  NextCheckAt: string;
  CheckIntervalSec: number;
  ConsecutiveErrors: number;
  Status: "active" | "paused" | "error";
  LastError: string;
  LastErrorCode: string;
  CreatedAt: string;
  UpdatedAt: string;
};

export type SystemInfo = {
  version: { version: string; commit: string; buildDate: string };
  trackers: {
    name: string;
    display_name: string;
    supports_interactive_login: boolean;
    supports_credentials: boolean;
  }[];
  clients: { name: string; display_name: string }[];
  notifiers: { name: string; display_name: string }[];
};
