import { LogOut } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/lib/auth-store";
import { useLogout } from "@/lib/hooks/useLogout";
import { useT } from "@/i18n";

/** Current user + sign-out, shared by the desktop sidebar and the mobile drawer. */
export function SidebarFooter() {
  const user = useAuthStore((s) => s.user);
  const t = useT();
  const handleLogout = useLogout();

  return (
    <div className="border-t border-border/60 p-4">
      <div className="mb-3 flex items-center gap-3">
        <div className="flex size-9 items-center justify-center rounded-full bg-primary/15 text-sm font-semibold text-primary">
          {user?.username?.[0]?.toUpperCase() ?? "?"}
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{user?.username ?? "unknown"}</div>
          <div className="truncate text-xs text-muted-foreground">{user?.role ?? ""}</div>
        </div>
      </div>
      <Button variant="outline" size="sm" className="w-full" onClick={handleLogout}>
        <LogOut className="size-4" />
        {t("login.signOut")}
      </Button>
    </div>
  );
}
