import { useEffect } from "react";
import { useNavigate } from "react-router-dom";

import { useAuthStore } from "@/lib/auth-store";
import { api, type Me, type TokenPair } from "@/lib/api";

/**
 * Lands here from the backend's /auth/oidc/callback redirect:
 *
 *   /oidc-callback#access_token=...&refresh_token=...&access_expires=...&refresh_expires=...
 *
 * The fragment carries the tokens (never sent to the backend), so we
 * parse them out, push them into the auth store, fetch /me, and bounce
 * to the dashboard.
 */
export function OIDCCallbackPage() {
  const navigate = useNavigate();
  const setTokens = useAuthStore((s) => s.setTokens);
  const setUser = useAuthStore((s) => s.setUser);

  useEffect(() => {
    const frag = window.location.hash.replace(/^#/, "");
    const params = new URLSearchParams(frag);
    const access = params.get("access_token");
    const refresh = params.get("refresh_token");
    const accessExp = params.get("access_expires");
    const refreshExp = params.get("refresh_expires");

    if (!access || !refresh || !accessExp || !refreshExp) {
      navigate("/login", { replace: true });
      return;
    }
    const pair: TokenPair = {
      access_token: access,
      refresh_token: refresh,
      access_token_expires_at: accessExp,
      refresh_token_expires_at: refreshExp,
      token_type: "Bearer",
    };
    setTokens(pair);
    api
      .get<Me>("/auth/me")
      .then((me) => {
        setUser(me);
        navigate("/", { replace: true });
      })
      .catch(() => navigate("/login", { replace: true }));
  }, [navigate, setTokens, setUser]);

  return (
    <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">
      Signing you in...
    </div>
  );
}
