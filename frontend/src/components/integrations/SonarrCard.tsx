import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { Field, CheckRow } from "@/components/integrations/SonarrFields";
import {
  EMPTY_FORM,
  errText,
  fromConfig,
  toUpdate,
  type SonarrForm,
} from "@/components/integrations/sonarrForm";

interface ClientOption {
  id: string;
  display_name: string;
}
type ClientsList = { clients: ClientOption[] | null };

/**
 * Admin-only Sonarr integration config. Rendered by the Integrations page,
 * which gates it to admins.
 */
export function SonarrCard() {
  const t = useT();
  const qc = useQueryClient();
  const { data: systemInfo } = useSystemInfo();
  const trackers = systemInfo?.trackers ?? [];

  const { data: config } = useQuery({ queryKey: QK.sonarrConfig, queryFn: api.getSonarrConfig });
  const { data: clientsData } = useQuery({
    queryKey: QK.clients,
    queryFn: () => api.get<ClientsList>("/clients"),
  });
  const clients = clientsData?.clients ?? [];

  const [form, setForm] = useState<SonarrForm>(EMPTY_FORM);
  const [keyStored, setKeyStored] = useState(false);
  const [message, setMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (config) {
      setForm(fromConfig(config));
      setKeyStored(config.api_key_set);
    }
  }, [config]);

  const update = <K extends keyof SonarrForm>(k: K, v: SonarrForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const toggleTracker = (name: string) =>
    setForm((f) => ({
      ...f,
      allowed_trackers: f.allowed_trackers.includes(name)
        ? f.allowed_trackers.filter((n) => n !== name)
        : [...f.allowed_trackers, name],
    }));

  const saveMut = useMutation({
    mutationFn: () => api.updateSonarrConfig(toUpdate(form)),
    onSuccess: () => {
      setMessage({ kind: "ok", text: t("settings.sonarr.saved") });
      qc.invalidateQueries({ queryKey: QK.sonarrConfig });
    },
    onError: (e) => setMessage({ kind: "err", text: errText(e) }),
  });

  const testMut = useMutation({
    mutationFn: () => api.testSonarr({ sonarr_url: form.sonarr_url, api_key: form.api_key }),
    onSuccess: (r) =>
      setMessage({ kind: "ok", text: `${t("settings.sonarr.testOk")} ${r.version}` }),
    onError: (e) => setMessage({ kind: "err", text: errText(e) }),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.sonarr.title")}</CardTitle>
        <CardDescription>{t("settings.sonarr.blurb")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <CheckRow
          label={t("settings.sonarr.enabled")}
          checked={form.enabled}
          onChange={(v) => update("enabled", v)}
        />

        <Field label={t("settings.sonarr.url")}>
          <Input
            value={form.sonarr_url}
            placeholder="http://sonarr:8989"
            onChange={(e) => update("sonarr_url", e.target.value)}
          />
        </Field>

        <Field label={t("settings.sonarr.apiKey")}>
          <Input
            type="password"
            value={form.api_key}
            placeholder={keyStored ? t("settings.sonarr.apiKeySet") : ""}
            onChange={(e) => update("api_key", e.target.value)}
            autoComplete="off"
          />
        </Field>

        <Field label={t("settings.sonarr.pollInterval")}>
          <Input
            type="number"
            min={60}
            value={form.poll_interval_sec}
            onChange={(e) => update("poll_interval_sec", Number(e.target.value))}
          />
        </Field>

        <Field label={t("settings.sonarr.defaultClient")}>
          <select
            value={form.default_client_id}
            onChange={(e) => update("default_client_id", e.target.value)}
            className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
          >
            <option value="">{t("settings.sonarr.defaultClientNone")}</option>
            {clients.map((c) => (
              <option key={c.id} value={c.id}>
                {c.display_name}
              </option>
            ))}
          </select>
        </Field>

        <Field label={t("settings.sonarr.defaultCategory")}>
          <Input
            value={form.default_category}
            placeholder="tv-sonarr"
            onChange={(e) => update("default_category", e.target.value)}
          />
        </Field>

        <Field label={t("settings.sonarr.defaultDownloadDir")}>
          <Input
            value={form.default_download_dir}
            onChange={(e) => update("default_download_dir", e.target.value)}
          />
        </Field>

        <div className="space-y-2">
          <span className="text-sm font-medium text-foreground">
            {t("settings.sonarr.allowedTrackers")}
          </span>
          <p className="text-xs text-muted-foreground">
            {t("settings.sonarr.allowedTrackersHint")}
          </p>
          <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
            {trackers.map((tr) => (
              <label key={tr.name} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.allowed_trackers.includes(tr.name)}
                  onChange={() => toggleTracker(tr.name)}
                />
                <span>{tr.display_name}</span>
              </label>
            ))}
          </div>
        </div>

        <CheckRow
          label={t("settings.sonarr.updateExisting")}
          checked={form.update_existing}
          onChange={(v) => update("update_existing", v)}
        />

        {message && (
          <div
            className={cn(
              "rounded-md border px-3 py-2 text-xs",
              message.kind === "ok"
                ? "border-success/40 bg-success/10 text-success"
                : "border-destructive/40 bg-destructive/10 text-destructive",
            )}
          >
            {message.text}
          </div>
        )}

        <div className="flex gap-3 border-t border-border/50 pt-5">
          <Button onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
            {saveMut.isPending ? t("settings.sonarr.saving") : t("settings.sonarr.save")}
          </Button>
          <Button
            variant="outline"
            onClick={() => testMut.mutate()}
            disabled={testMut.isPending}
          >
            {testMut.isPending ? t("settings.sonarr.testing") : t("settings.sonarr.test")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
