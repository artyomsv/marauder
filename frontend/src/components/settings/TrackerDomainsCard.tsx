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

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Globe className="size-4 text-muted-foreground" />
          {t("settings.domains.title")}
        </CardTitle>
        <CardDescription>
          {t("settings.domains.blurb")} {t("settings.domains.instanceWideNote")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {trackers.map((tr) => (
          <TrackerDomainRow key={tr.name} tracker={tr} />
        ))}
      </CardContent>
    </Card>
  );
}
