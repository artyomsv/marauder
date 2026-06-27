import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Server } from "lucide-react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { useT } from "@/i18n";
import { SonarrInstanceCard } from "@/components/integrations/SonarrInstanceCard";
import { SonarrInstanceForm, type ClientOption } from "@/components/integrations/SonarrInstanceForm";

type ClientsList = { clients: ClientOption[] | null };

/**
 * Lists the configured Sonarr instances as cards, with an inline add form and
 * an empty-state call to action. Owns the shared clients + trackers lookups so
 * each card/form doesn't refetch them.
 */
export function SonarrInstanceList() {
  const t = useT();
  const { data: systemInfo } = useSystemInfo();
  const trackers = systemInfo?.trackers ?? [];

  const { data: instances } = useQuery({
    queryKey: QK.sonarrInstances,
    queryFn: api.listSonarrInstances,
  });
  const { data: clientsData } = useQuery({
    queryKey: QK.clients,
    queryFn: () => api.get<ClientsList>("/clients"),
  });
  const clients = clientsData?.clients ?? [];

  const [adding, setAdding] = useState(false);

  const list = instances ?? [];

  return (
    <section className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Server className="size-5 text-muted-foreground" />
          <h2 className="text-lg font-semibold tracking-tight">{t("settings.sonarr.title")}</h2>
        </div>
        {!adding && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus className="mr-1.5 size-4" />
            {t("settings.sonarr.instances.add")}
          </Button>
        )}
      </div>
      <p className="text-sm text-muted-foreground">{t("settings.sonarr.blurb")}</p>

      {adding && (
        <Card>
          <CardHeader>
            <CardTitle>{t("settings.sonarr.instances.addTitle")}</CardTitle>
          </CardHeader>
          <CardContent>
            <SonarrInstanceForm
              initial={null}
              clients={clients}
              trackers={trackers}
              onDone={() => setAdding(false)}
              onCancel={() => setAdding(false)}
            />
          </CardContent>
        </Card>
      )}

      {list.length === 0 && !adding ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <Server className="size-8 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">{t("settings.sonarr.instances.empty")}</p>
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus className="mr-1.5 size-4" />
              {t("settings.sonarr.instances.add")}
            </Button>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {list.map((inst) => (
            <SonarrInstanceCard key={inst.id} instance={inst} clients={clients} trackers={trackers} />
          ))}
        </div>
      )}
    </section>
  );
}
