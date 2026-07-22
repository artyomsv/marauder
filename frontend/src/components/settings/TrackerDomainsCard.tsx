import { useQuery } from "@tanstack/react-query";
import { Globe } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { TrackerDomainRow } from "@/components/settings/TrackerDomainRow";

/**
 * Admin-only "Tracker domains" settings section (issue #126). Lets an admin
 * override which domain each tracker plugin uses — e.g. when its default
 * mirror is blocked — and register additional custom mirrors, instance-wide.
 */
export function TrackerDomainsCard() {
  const t = useT();
  const { data } = useQuery({ queryKey: QK.trackerDomains, queryFn: api.listTrackerDomains });
  const trackers = data ?? [];
  const overridden = trackers.filter(
    (tr) => tr.active_domain !== "" && tr.active_domain !== tr.default_domain,
  ).length;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2">
              <Globe className="size-4 text-muted-foreground" />
              {t("settings.domains.title")}
            </CardTitle>
            <CardDescription>
              {t("settings.domains.blurb")} {t("settings.domains.instanceWideNote")}
            </CardDescription>
          </div>
          {trackers.length > 0 && (
            <span className="shrink-0 whitespace-nowrap font-mono text-xs text-muted-foreground">
              {t("settings.domains.summary", { overridden, total: trackers.length })}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        {trackers.map((tr) => (
          <TrackerDomainRow key={tr.name} tracker={tr} />
        ))}
      </CardContent>
    </Card>
  );
}
