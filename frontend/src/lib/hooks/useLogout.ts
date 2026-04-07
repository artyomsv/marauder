import { useNavigate } from "react-router-dom";

import { api } from "@/lib/api";
import { useAuthStore } from "@/lib/auth-store";

/**
 * Returns a `logout` function that performs the full sign-out sequence:
 *
 *   1. Best-effort POST `/auth/logout` to revoke the refresh token
 *      server-side. Failures are swallowed — the user is logged out
 *      locally regardless of whether the server is reachable.
 *   2. Clear the auth store (tokens + cached `Me`).
 *   3. Navigate to `/login`.
 *
 * Used by the AppShell sidebar sign-out button and the Settings
 * Account card sign-out button.
 */
export function useLogout() {
  const navigate = useNavigate();
  const refreshToken = useAuthStore((s) => s.refreshToken);
  const clearStore = useAuthStore((s) => s.logout);

  return async function logout() {
    if (refreshToken) {
      try {
        await api.post("/auth/logout", { refresh_token: refreshToken });
      } catch {
        // Best-effort: server may be unreachable. Local logout proceeds.
      }
    }
    clearStore();
    navigate("/login");
  };
}
