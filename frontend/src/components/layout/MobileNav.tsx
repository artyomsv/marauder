import { useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import { motion } from "framer-motion";
import { Menu, X } from "lucide-react";

import { useT } from "@/i18n";
import { NavLinks } from "@/components/layout/NavLinks";
import { SidebarFooter } from "@/components/layout/SidebarFooter";

const PANEL_ID = "mobile-nav";

/**
 * Menu button + slide-out drawer for screens below the `md` breakpoint,
 * where the fixed sidebar is hidden (issue #199). The drawer renders the
 * same NavLinks and SidebarFooter as the sidebar. It closes on a link
 * press, on Escape, on the close button, on a backdrop press, and on any
 * route change (a browser back button included).
 *
 * The panel is conditionally rendered rather than wrapped in AnimatePresence:
 * an exit animation would keep the dialog in the DOM after "close", which
 * is wrong for screen readers and for tests alike. Entry still animates.
 */
export function MobileNav() {
  const t = useT();
  const { pathname } = useLocation();
  const [isOpen, setIsOpen] = useState(false);

  // Any navigation closes the drawer, including ones not started from it.
  useEffect(() => {
    setIsOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!isOpen) return;
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === "Escape") setIsOpen(false);
    };
    document.addEventListener("keydown", handleEscape);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", handleEscape);
      document.body.style.overflow = previousOverflow;
    };
  }, [isOpen]);

  const close = () => setIsOpen(false);

  return (
    <div className="md:hidden">
      <button
        type="button"
        aria-label={t("nav.openMenu")}
        aria-expanded={isOpen}
        aria-controls={PANEL_ID}
        onClick={() => setIsOpen(true)}
        className="inline-flex items-center justify-center rounded-md p-2 text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Menu className="size-5" />
      </button>

      {isOpen && (
        <div className="fixed inset-0 z-40">
          <div
            data-testid="mobile-nav-backdrop"
            aria-hidden="true"
            onClick={close}
            className="absolute inset-0 bg-background/60 backdrop-blur-sm"
          />
          <motion.div
            id={PANEL_ID}
            role="dialog"
            aria-modal="true"
            aria-label={t("nav.menu")}
            initial={{ x: -24, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            transition={{ duration: 0.18, ease: "easeOut" }}
            className="absolute inset-y-0 left-0 flex w-72 flex-col border-r border-border/60 bg-background/95 shadow-2xl backdrop-blur-xl"
          >
            <div className="flex h-16 items-center justify-between border-b border-border/60 px-4">
              <span className="font-mono text-sm font-semibold tracking-tight text-foreground">
                marauder
              </span>
              <button
                type="button"
                aria-label={t("nav.closeMenu")}
                onClick={close}
                className="inline-flex items-center justify-center rounded-md p-2 text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <X className="size-5" />
              </button>
            </div>
            <NavLinks onNavigate={close} />
            <SidebarFooter />
          </motion.div>
        </div>
      )}
    </div>
  );
}
