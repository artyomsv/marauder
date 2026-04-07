import { useEffect } from "react";
import {
  BrowserRouter,
  Navigate,
  Outlet,
  Route,
  Routes,
} from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { AppShell } from "@/components/layout/AppShell";
import { LoginPage } from "@/pages/Login";
import { DashboardPage } from "@/pages/Dashboard";
import { TopicsPage } from "@/pages/Topics";
import { ClientsPage } from "@/pages/Clients";
import { NotifiersPage } from "@/pages/Notifiers";
import { SystemPage } from "@/pages/System";
import { AuditPage } from "@/pages/Audit";
import { PlaceholderPage } from "@/pages/Placeholder";
import { useAuthStore } from "@/lib/auth-store";
import { api, type Me } from "@/lib/api";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 30_000,
    },
  },
});

function ProtectedLayout() {
  const accessToken = useAuthStore((s) => s.accessToken);
  const setUser = useAuthStore((s) => s.setUser);
  const logout = useAuthStore((s) => s.logout);
  const user = useAuthStore((s) => s.user);

  useEffect(() => {
    if (!accessToken) return;
    if (user) return;
    api
      .get<Me>("/auth/me")
      .then((me) => setUser(me))
      .catch(() => logout());
  }, [accessToken, user, setUser, logout]);

  if (!accessToken) return <Navigate to="/login" replace />;
  return <Outlet />;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<ProtectedLayout />}>
            <Route element={<AppShell />}>
              <Route index element={<DashboardPage />} />
              <Route path="topics" element={<TopicsPage />} />
              <Route path="clients" element={<ClientsPage />} />
              <Route path="notifiers" element={<NotifiersPage />} />
              <Route path="system" element={<SystemPage />} />
              <Route path="audit" element={<AuditPage />} />
              <Route
                path="settings"
                element={
                  <PlaceholderPage
                    title="Settings"
                    blurb="Global preferences, scheduler interval, OIDC configuration, theme."
                  />
                }
              />
            </Route>
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
