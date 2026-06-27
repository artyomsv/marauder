import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Loader2, Pencil } from "lucide-react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { api, type SonarrInstance } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { instanceToBody } from "@/components/integrations/sonarrForm";
import { SonarrInstanceForm, type ClientOption } from "@/components/integrations/SonarrInstanceForm";

interface TrackerOption {
  name: string;
  display_name: string;
}

interface Props {
  instance: SonarrInstance;
  clients: ClientOption[];
  trackers: TrackerOption[];
}

type Status = "active" | "paused" | "draft";

function statusOf(i: SonarrInstance): Status {
  if (i.enabled) return "active";
  if (i.last_seen_at) return "paused";
  return "draft";
}

const STATUS_VARIANT: Record<Status, "success" | "secondary" | "warning"> = {
  active: "success",
  paused: "secondary",
  draft: "warning",
};

/**
 * One Sonarr instance: a read view with a status badge and row of actions
 * (enable/disable, test, edit, delete). Editing swaps the body for the inline
 * form in place.
 */
export function SonarrInstanceCard({ instance, clients, trackers }: Props) {
  const t = useT();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [testMsg, setTestMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const status = statusOf(instance);

  const toggleMut = useMutation({
    mutationFn: () =>
      api.updateSonarrInstance(instance.id, {
        ...instanceToBody(instance),
        enabled: !instance.enabled,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.sonarrInstances }),
  });

  const deleteMut = useMutation({
    mutationFn: () => api.deleteSonarrInstance(instance.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.sonarrInstances }),
  });

  const testMut = useMutation({
    mutationFn: () => api.testSonarrInstance(instance.id),
    onSuccess: (r) => setTestMsg({ kind: "ok", text: `${t("settings.sonarr.testOk")} ${r.version}` }),
    onError: () => setTestMsg({ kind: "err", text: t("settings.sonarr.testFailed") }),
  });

  if (editing) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{instance.name}</CardTitle>
        </CardHeader>
        <CardContent>
          <SonarrInstanceForm
            initial={instance}
            clients={clients}
            trackers={trackers}
            onDone={() => setEditing(false)}
            onCancel={() => setEditing(false)}
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2">
            {instance.name}
            <Badge variant={STATUS_VARIANT[status]}>{t(`settings.sonarr.status.${status}`)}</Badge>
          </CardTitle>
          <p className="text-xs text-muted-foreground">{instance.sonarr_url || "—"}</p>
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon" aria-label={t("settings.sonarr.actions.edit")} onClick={() => setEditing(true)}>
            <Pencil className="size-4" />
          </Button>
          <DeleteConfirm
            onConfirm={() => deleteMut.mutate()}
            isPending={deleteMut.isPending}
            label={t("settings.sonarr.actions.delete")}
          />
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <dt>{t("settings.sonarr.defaultCategory")}</dt>
          <dd className="text-foreground">{instance.default_category || "—"}</dd>
          <dt>{t("settings.sonarr.lastSeen")}</dt>
          <dd className="text-foreground">
            {instance.last_seen_at ? new Date(instance.last_seen_at).toLocaleString() : "—"}
          </dd>
        </dl>

        {testMsg && (
          <p className={testMsg.kind === "ok" ? "text-xs text-success" : "text-xs text-destructive"}>
            {testMsg.text}
          </p>
        )}

        <div className="flex flex-wrap gap-2 border-t border-border/50 pt-3">
          <Button
            variant={instance.enabled ? "outline" : "default"}
            size="sm"
            onClick={() => toggleMut.mutate()}
            disabled={toggleMut.isPending}
          >
            {toggleMut.isPending && <Loader2 className="mr-1.5 size-3.5 animate-spin" />}
            {instance.enabled ? t("settings.sonarr.actions.disable") : t("settings.sonarr.actions.enable")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
            {testMut.isPending ? (
              <Loader2 className="mr-1.5 size-3.5 animate-spin" />
            ) : (
              <CheckCircle2 className="mr-1.5 size-3.5" />
            )}
            {t("settings.sonarr.test")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
