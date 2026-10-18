import { useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import * as Dialog from "@radix-ui/react-dialog";
import { motion } from "framer-motion";
import { Menu, X } from "lucide-react";

import { useT } from "@/i18n";
import { NavLinks } from "@/components/layout/NavLinks";
import { SidebarFooter } from "@/components/layout/SidebarFooter";

/** Tailwind's `md` breakpoint (48rem). Above it the desktop sidebar takes over. */
const DESKTOP_MEDIA_QUERY = "(min-width: 48rem)";

const iconButtonClass =
  "inline-flex items-center justify-center rounded-md p-2 text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

/**
 * Menu button + slide-out drawer for screens below the `md` breakpoint,
 * where the fixed sidebar is hidden (issue #199). The drawer renders the
 * same NavLinks and SidebarFooter as the sidebar.
 *
 * Built on Radix Dialog rather than by hand, after review of the first
 * version found what a hand-rolled overlay gets wrong:
 * - It renders through a portal. The header it lives in has
 *   `backdrop-blur`, and a backdrop filter makes an ancestor the
 *   containing block of `position: fixed` descendants, so an in-place
 *   `fixed inset-0` overlay filled the 64px header instead of the screen.
 * - Focus moves into the dialog, stays inside on Tab, and returns to the
 *   menu button on close. Escape and a press outside close it.
 * - Body scroll is locked while open.
 *
 * The dialog also closes on any route change (a browser back button
 * included) and when the viewport grows past `md`: the portal escapes the
 * header's `md:hidden`, and a dialog hidden by CSS alone would keep the
 * page scroll-locked and the focus trapped inside something invisible.
 */
export function MobileNav() {
  const t = useT();
  const { pathname } = useLocation();
  const [isOpen, setIsOpen] = useState(false);

  useEffect(() => {
    setIsOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!isOpen || typeof window.matchMedia !== "function") return;
    const desktop = window.matchMedia(DESKTOP_MEDIA_QUERY);
    const handleChange = (e: MediaQueryListEvent) => {
      if (e.matches) setIsOpen(false);
    };
    desktop.addEventListener("change", handleChange);
    return () => desktop.removeEventListener("change", handleChange);
  }, [isOpen]);

  const close = () => setIsOpen(false);

  return (
    <Dialog.Root open={isOpen} onOpenChange={setIsOpen}>
      <Dialog.Trigger asChild>
        <button type="button" aria-label={t("nav.openMenu")} className={iconButtonClass}>
          <Menu className="size-5" />
        </button>
      </Dialog.Trigger>

      <Dialog.Portal>
        <Dialog.Overlay
          data-testid="mobile-nav-backdrop"
          className="fixed inset-0 z-40 bg-background/60 backdrop-blur-sm md:hidden"
        />
        <Dialog.Content asChild aria-describedby={undefined}>
          <motion.div
            initial={{ x: -24, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            transition={{ duration: 0.18, ease: "easeOut" }}
            className="fixed inset-y-0 left-0 z-50 flex w-72 flex-col border-r border-border/60 bg-background/95 shadow-2xl backdrop-blur-xl focus:outline-none md:hidden"
          >
            <div className="flex h-16 shrink-0 items-center justify-between border-b border-border/60 px-4">
              <Dialog.Title className="sr-only">{t("nav.menu")}</Dialog.Title>
              <span className="font-mono text-sm font-semibold tracking-tight text-foreground">
                marauder
              </span>
              <Dialog.Close asChild>
                <button type="button" aria-label={t("nav.closeMenu")} className={iconButtonClass}>
                  <X className="size-5" />
                </button>
              </Dialog.Close>
            </div>
            <NavLinks onNavigate={close} />
            <SidebarFooter />
          </motion.div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
