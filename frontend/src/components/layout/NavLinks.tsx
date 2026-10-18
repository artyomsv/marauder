import { NavLink } from "react-router-dom";
import { Activity, Bell, KeyRound, LayoutDashboard, Plug, Radio, Server, Settings, Shield } from "lucide-react";

import { cn } from "@/lib/utils";
import { useAuthStore } from "@/lib/auth-store";
import { useT } from "@/i18n";

interface NavItem {
  to: string;
  labelKey: string;
  icon: typeof LayoutDashboard;
  adminOnly?: boolean;
}

const navItems: NavItem[] = [
  { to: "/", labelKey: "nav.dashboard", icon: LayoutDashboard },
  { to: "/topics", labelKey: "nav.topics", icon: Radio },
  { to: "/clients", labelKey: "nav.clients", icon: Server },
  { to: "/accounts", labelKey: "nav.accounts", icon: KeyRound },
  { to: "/notifiers", labelKey: "nav.notifiers", icon: Bell },
  { to: "/system", labelKey: "nav.system", icon: Activity },
  { to: "/audit", labelKey: "nav.audit", icon: Shield, adminOnly: true },
  { to: "/integrations", labelKey: "nav.integrations", icon: Plug, adminOnly: true },
  { to: "/settings", labelKey: "nav.settings", icon: Settings },
];

interface NavLinksProps {
  /** Called after a link is activated — the mobile drawer closes itself with it. */
  onNavigate?: () => void;
}

/**
 * The page list shared by the desktop sidebar and the mobile drawer, so
 * the two can never drift apart (issue #199: the drawer did not exist and
 * small screens had no way to reach any page).
 */
export function NavLinks({ onNavigate }: NavLinksProps) {
  const user = useAuthStore((s) => s.user);
  const t = useT();

  // min-h-0 + overflow-y-auto: on a short viewport (a phone in landscape)
  // the list is taller than the drawer, and the drawer locks page scroll,
  // so without its own scrolling the lower pages and sign-out are unreachable.
  return (
    <nav className="min-h-0 flex-1 space-y-1 overflow-y-auto p-3">
      {navItems
        .filter((item) => !item.adminOnly || user?.role === "admin")
        .map(({ to, labelKey, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === "/"}
            onClick={onNavigate}
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
  );
}
