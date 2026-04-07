import type { ReactNode } from "react";
import { motion } from "framer-motion";

import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

/**
 * Color variants for the soft blur "glow" decoration in the top-right
 * corner of the card. The blob is purely cosmetic but each list page
 * picked a slightly different gradient, so we keep both options
 * available rather than flatten them.
 */
type GlowVariant = "primary" | "accent";

interface ResourceCardProps {
  /** Primary heading rendered in the title row. Usually a string but
   *  accepts ReactNode for inline icons or styled spans. */
  title: ReactNode;
  /** Badges rendered to the right of the title (plugin name badge,
   *  default flag, channel events, username, etc). */
  badges?: ReactNode;
  /** Action buttons (Edit, DeleteConfirm). Rendered top-right of the
   *  card and only visible on hover via group-hover. */
  actions?: ReactNode;
  /** Body content under the title row — typically the "Test connection"
   *  button row plus any inline error message. */
  children?: ReactNode;
  /** Picks which gradient is used for the corner blur. Notifiers uses
   *  "accent" to differentiate from clients/credentials which both use
   *  the default "primary". */
  glow?: GlowVariant;
  /** Optional click handler on the entire card. */
  onClick?: () => void;
}

/**
 * ResourceCard is the shared chrome behind the Clients, Credentials,
 * and Notifiers list pages. Each page renders a list of similar items:
 * a glassy card with a corner glow, a title row (label + badges +
 * hover-revealed actions), and a body slot for test/connection
 * controls.
 *
 * The component is intentionally slot-based — every page's title,
 * badges, actions, and body differ slightly, but the card frame and
 * the entrance animation are identical. Only the chrome lives here.
 */
export function ResourceCard({
  title,
  badges,
  actions,
  children,
  glow = "primary",
  onClick,
}: ResourceCardProps) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
    >
      <Card
        className="group relative overflow-hidden"
        onClick={onClick}
      >
        <div
          className={cn(
            "pointer-events-none absolute -right-12 -top-12 size-40 rounded-full blur-2xl",
            glow === "accent"
              ? "bg-gradient-to-br from-accent/30 to-primary/10"
              : "bg-gradient-to-br from-primary/30 to-accent/10",
          )}
        />
        <div className="relative p-6">
          <div className="mb-2 flex items-start justify-between gap-2">
            <div className="min-w-0">
              <div className="text-base font-semibold">{title}</div>
              {badges && (
                <div className="mt-0.5 flex flex-wrap items-center gap-2">
                  {badges}
                </div>
              )}
            </div>
            {actions && (
              <div className="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
                {actions}
              </div>
            )}
          </div>
          {children}
        </div>
      </Card>
    </motion.div>
  );
}
