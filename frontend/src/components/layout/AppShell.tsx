import { Link, NavLink, Outlet } from "react-router-dom";
import { LogOut, Radio, LayoutDashboard, Server, Bell, Settings, KeyRound, Moon, Sun, Activity, Shield } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/lib/auth-store";
import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { useLogout } from "@/lib/hooks/useLogout";
import { useT } from "@/i18n";
import { usePrefs } from "@/lib/prefs";
import { LocaleSwitcher } from "@/components/layout/LocaleSwitcher";

type NavItem = {
  to: string;
  labelKey: string;
  icon: typeof LayoutDashboard;
  adminOnly?: boolean;
};

const navItems: NavItem[] = [
  { to: "/", labelKey: "nav.dashboard", icon: LayoutDashboard },
  { to: "/topics", labelKey: "nav.topics", icon: Radio },
  { to: "/clients", labelKey: "nav.clients", icon: Server },
  { to: "/accounts", labelKey: "nav.accounts", icon: KeyRound },
  { to: "/notifiers", labelKey: "nav.notifiers", icon: Bell },
  { to: "/system", labelKey: "nav.system", icon: Activity },
  { to: "/audit", labelKey: "nav.audit", icon: Shield, adminOnly: true },
  { to: "/settings", labelKey: "nav.settings", icon: Settings },
];

export function AppShell() {
  const user = useAuthStore((s) => s.user);
  const t = useT();
  const theme = usePrefs((s) => s.theme);
  const setTheme = usePrefs((s) => s.setTheme);
  const { data: systemInfo } = useSystemInfo();
  const version = systemInfo?.version?.version ?? "";
  const handleLogout = useLogout();

  return (
    <div className="flex min-h-screen">
      {/* Sidebar */}
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-64 flex-col border-r border-border/60 bg-background/30 backdrop-blur-xl md:flex">
        <div className="flex h-16 items-center gap-3 border-b border-border/60 px-6">
          <Logo />
          <span className="font-mono text-sm font-semibold tracking-tight text-foreground">
            marauder
          </span>
          <span className="ml-auto font-mono text-[10px] text-muted-foreground">
            {version ? `v${version}` : ""}
          </span>
        </div>

        <nav className="flex-1 space-y-1 p-3">
          {navItems
            .filter((item) => !item.adminOnly || user?.role === "admin")
            .map(({ to, labelKey, icon: Icon }) => (
              <NavLink
                key={to}
                to={to}
                end={to === "/"}
                className={({ isActive }) =>
                  cn(
                    "group relative flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                    isActive
                      ? "bg-primary/10 text-foreground shadow-[inset_0_0_0_1px_hsl(var(--primary)/0.25)]"
                      : "text-muted-foreground hover:bg-accent/5 hover:text-foreground",
                  )
                }
              >
                <Icon className="size-4" />
                <span>{t(labelKey)}</span>
              </NavLink>
            ))}
        </nav>

        <div className="border-t border-border/60 p-4">
          <div className="mb-3 flex items-center gap-3">
            <div className="flex size-9 items-center justify-center rounded-full bg-primary/15 text-sm font-semibold text-primary">
              {user?.username?.[0]?.toUpperCase() ?? "?"}
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">
                {user?.username ?? "unknown"}
              </div>
              <div className="truncate text-xs text-muted-foreground">
                {user?.role ?? ""}
              </div>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="w-full"
            onClick={handleLogout}
          >
            <LogOut className="size-4" />
            {t("login.signOut")}
          </Button>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 md:ml-64">
        <header className="sticky top-0 z-20 flex h-16 items-center gap-4 border-b border-border/60 bg-background/40 px-6 backdrop-blur-xl">
          <div className="flex md:hidden items-center gap-2">
            <Logo />
            <span className="font-mono text-sm font-semibold">marauder</span>
          </div>
          <div className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
            <LocaleSwitcher />
            <span className="size-1 rounded-full bg-border" />
            <button
              type="button"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
              className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {theme === "dark" ? <Moon className="size-3.5" /> : <Sun className="size-3.5" />}
              <span>{theme}</span>
            </button>
            <span className="size-1 rounded-full bg-border" />
            <Link to="https://marauder.cc" className="hover:text-foreground">
              marauder.cc
            </Link>
          </div>
        </header>
        <div className="mx-auto max-w-7xl p-6 md:p-10">
          <Outlet />
        </div>
      </main>
    </div>
  );
}

function Logo() {
  return (
    <div className="relative flex size-8 items-center justify-center">
      <div className="absolute inset-0 rounded-lg bg-gradient-to-br from-primary via-primary to-accent opacity-90 blur-[2px]" />
      <svg viewBox="0 0 64 64" className="relative size-6 text-primary-foreground">
        <path
          d="M16 44 L16 20 L28 36 L32 30 L36 36 L48 20 L48 44"
          fill="none"
          stroke="currentColor"
          strokeWidth="5"
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      </svg>
    </div>
  );
}
