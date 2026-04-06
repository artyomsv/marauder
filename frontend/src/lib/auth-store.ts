/**
 * Zustand store for auth state.
 *
 * Access and refresh tokens live in localStorage so they survive a page
 * reload. When v1.x lands we'll harden this with a short-lived access
 * token in memory only and a refresh token in an HttpOnly cookie; for
 * v0.1 we're optimising for simplicity.
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";

import type { Me, TokenPair } from "@/lib/api";

type AuthState = {
  accessToken: string | null;
  refreshToken: string | null;
  accessTokenExpiresAt: string | null;
  refreshTokenExpiresAt: string | null;
  user: Me | null;

  setTokens: (t: TokenPair) => void;
  setUser: (u: Me | null) => void;
  logout: () => void;
  isAuthed: () => boolean;
};

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      accessToken: null,
      refreshToken: null,
      accessTokenExpiresAt: null,
      refreshTokenExpiresAt: null,
      user: null,

      setTokens: (t) =>
        set({
          accessToken: t.access_token,
          refreshToken: t.refresh_token,
          accessTokenExpiresAt: t.access_token_expires_at,
          refreshTokenExpiresAt: t.refresh_token_expires_at,
        }),
      setUser: (u) => set({ user: u }),
      logout: () =>
        set({
          accessToken: null,
          refreshToken: null,
          accessTokenExpiresAt: null,
          refreshTokenExpiresAt: null,
          user: null,
        }),
      isAuthed: () => !!get().accessToken,
    }),
    {
      name: "marauder-auth",
    },
  ),
);
