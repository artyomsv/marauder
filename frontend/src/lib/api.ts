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
};

// --- Typed models mirroring backend/internal/domain ---------------------

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

export type Topic = {
  ID: string;
  UserID: string;
  TrackerName: string;
  URL: string;
  DisplayName: string;
  ClientID: string | null;
  DownloadDir: string;
  LastHash: string;
  LastCheckedAt: string | null;
  LastUpdatedAt: string | null;
  NextCheckAt: string;
  CheckIntervalSec: number;
  ConsecutiveErrors: number;
  Status: "active" | "paused" | "error";
  LastError: string;
  CreatedAt: string;
  UpdatedAt: string;
};

export type SystemInfo = {
  version: { version: string; commit: string; buildDate: string };
  trackers: { name: string; display_name: string }[];
  clients: { name: string; display_name: string }[];
  notifiers: { name: string; display_name: string }[];
};
