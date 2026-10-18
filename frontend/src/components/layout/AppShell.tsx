import { Link, Outlet } from "react-router-dom";
import { Moon, Sun } from "lucide-react";

import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { usePrefs } from "@/lib/prefs";
import { LocaleSwitcher } from "@/components/layout/LocaleSwitcher";
import { MobileNav } from "@/components/layout/MobileNav";
import { NavLinks } from "@/components/layout/NavLinks";
import { SidebarFooter } from "@/components/layout/SidebarFooter";

export function AppShell() {
  const theme = usePrefs((s) => s.theme);
  const setTheme = usePrefs((s) => s.setTheme);
  const { data: systemInfo } = useSystemInfo();
  const version = systemInfo?.version?.version ?? "";

  return (
    <div className="flex min-h-screen">
      {/* Sidebar — desktop only; MobileNav in the header replaces it below md */}
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

        <NavLinks />
        <SidebarFooter />
      </aside>

      {/* Main */}
      {/* min-w-0: a flex item defaults to min-width:auto and won't shrink
          below its content's intrinsic width — a long topic title would then
          push main past the viewport. min-w-0 lets inner truncate/break-words
          constrain instead. */}
      {/* overflow-x-clip (not hidden): one element wider than a phone screen
          makes the mobile browser zoom the page out, which reads as uneven
          side margins. clip stops that without making main a scroll
          container, which would break the sticky header. */}
      <main className="min-w-0 flex-1 overflow-x-clip md:ml-64">
        <header className="sticky top-0 z-20 flex h-16 items-center gap-4 border-b border-border/60 bg-background/40 px-4 backdrop-blur-xl md:px-6">
          <div className="flex md:hidden items-center gap-2">
            <MobileNav />
            <Logo />
            <span className="font-mono text-sm font-semibold">marauder</span>
          </div>
          {/* Below sm the labels go and only the icons stay: with the menu
              button, logo and wordmark on the left, the full set did not fit. */}
          <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground sm:gap-3">
            <LocaleSwitcher />
            <span className="hidden size-1 rounded-full bg-border sm:block" />
            <button
              type="button"
              onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
              className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {theme === "dark" ? <Moon className="size-3.5" /> : <Sun className="size-3.5" />}
              <span className="hidden sm:inline">{theme}</span>
            </button>
            <span className="hidden size-1 rounded-full bg-border sm:block" />
            <Link to="https://marauder.cc" className="hidden hover:text-foreground sm:inline">
              marauder.cc
            </Link>
          </div>
        </header>
        <div className="mx-auto max-w-7xl p-4 sm:p-6 md:p-10">
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
