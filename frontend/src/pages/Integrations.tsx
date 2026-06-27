import { motion } from "framer-motion";
import { Navigate } from "react-router-dom";

import { useAuthStore } from "@/lib/auth-store";
import { SonarrInstanceList } from "@/components/integrations/SonarrInstanceList";
import { useT } from "@/i18n";

/**
 * Admin-only Integrations page — connects Marauder to external services.
 * Currently hosts the Sonarr integration; structured to hold more later.
 */
export function IntegrationsPage() {
  const t = useT();
  const user = useAuthStore((s) => s.user);

  // Integrations are admin-only. Non-admins who reach the URL directly are
  // bounced home (the nav entry is already hidden for them).
  if (user && user.role !== "admin") {
    return <Navigate to="/" replace />;
  }

  return (
    <div className="space-y-8">
      <motion.header
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.4 }}
      >
        <div className="mb-1 text-xs font-mono uppercase tracking-wider text-muted-foreground">
          {t("integrations.kicker")}
        </div>
        <h1 className="text-3xl font-semibold tracking-tight">{t("integrations.title")}</h1>
        <p className="mt-2 text-sm text-muted-foreground">{t("integrations.blurb")}</p>
      </motion.header>

      <SonarrInstanceList />
    </div>
  );
}
