import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Loader2, Plus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SELECT_CLASS } from "@/components/topics/SeasonEpisodePicker";
import { api, type TrackerDomains } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";

// Lowercased-trim hostname validation: RFC-1123-ish labels, at least two of
// them, no scheme/port/path. Mirrors the backend's validateHostname so a
// client-side reject never fires a request the server would 422 anyway.
const HOSTNAME_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;

interface Props {
  tracker: TrackerDomains;
}

type InlineMsg = { kind: "ok" | "err"; text: string };

// One tracker's domain row: a select of default + known + custom domains,
// an inline "add mirror" input, per-custom-domain removal, and a Test
// button that probes a candidate domain without saving it.
export function TrackerDomainRow({ tracker }: Props) {
  const t = useT();
  const qc = useQueryClient();
  const [newDomain, setNewDomain] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const [testMsg, setTestMsg] = useState<InlineMsg | null>(null);

  const updateMut = useMutation({
    mutationFn: (body: { active_domain: string; custom_domains: string[] }) =>
      api.updateTrackerDomains(tracker.name, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.trackerDomains }),
  });

  const testMut = useMutation({
    mutationFn: (domain: string) => api.testTrackerDomain(tracker.name, domain),
    onSuccess: (r) =>
      setTestMsg(
        r.ok
          ? { kind: "ok", text: t("settings.domains.testOk") }
          : { kind: "err", text: t("settings.domains.testFail") },
      ),
    onError: () => setTestMsg({ kind: "err", text: t("settings.domains.testFail") }),
  });

  const handleSelect = (e: React.ChangeEvent<HTMLSelectElement>) => {
    updateMut.mutate({ active_domain: e.target.value, custom_domains: tracker.custom_domains });
  };

  const handleAdd = () => {
    setAddError(null);
    const normalized = newDomain.trim().toLowerCase();
    if (!HOSTNAME_RE.test(normalized)) {
      setAddError(t("settings.domains.invalidHostname"));
      return;
    }
    updateMut.mutate({
      active_domain: tracker.active_domain,
      custom_domains: [...tracker.custom_domains, normalized],
    });
    setNewDomain("");
  };

  const handleRemove = (domain: string) => {
    updateMut.mutate({
      // Fall back to the plugin default if the removed domain was active.
      active_domain: tracker.active_domain === domain ? "" : tracker.active_domain,
      custom_domains: tracker.custom_domains.filter((d) => d !== domain),
    });
  };

  const handleTest = () => {
    setTestMsg(null);
    testMut.mutate(tracker.active_domain || tracker.default_domain);
  };

  // known_domains includes the default domain itself — drop it here so it
  // isn't offered twice (once via the "(default)" option, once bare).
  const alternatives = tracker.known_domains.filter((d) => d !== tracker.default_domain);
  const selectId = `tracker-domain-select-${tracker.name}`;

  return (
    <div
      data-testid={`domain-row-${tracker.name}`}
      className="space-y-2 border-b border-border/50 py-4 last:border-b-0 last:pb-0"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label htmlFor={selectId}>{tracker.display_name}</Label>
        <Button variant="outline" size="sm" onClick={handleTest} disabled={testMut.isPending}>
          {testMut.isPending ? (
            <Loader2 className="mr-1.5 size-3.5 animate-spin" />
          ) : (
            <CheckCircle2 className="mr-1.5 size-3.5" />
          )}
          {t("settings.domains.test")}
        </Button>
      </div>

      <select
        id={selectId}
        value={tracker.active_domain}
        onChange={handleSelect}
        className={SELECT_CLASS}
        disabled={updateMut.isPending}
      >
        <option value="">
          {tracker.default_domain} {t("settings.domains.defaultSuffix")}
        </option>
        {alternatives.map((d) => (
          <option key={d} value={d}>
            {d}
          </option>
        ))}
        {tracker.custom_domains.map((d) => (
          <option key={d} value={d}>
            {d}
          </option>
        ))}
      </select>

      {testMsg && (
        <p className={testMsg.kind === "ok" ? "text-xs text-success" : "text-xs text-destructive"}>
          {testMsg.text}
        </p>
      )}

      {tracker.custom_domains.length > 0 && (
        <ul className="flex flex-wrap gap-2">
          {tracker.custom_domains.map((d) => (
            <li
              key={d}
              className="flex items-center gap-1 rounded-full border border-border/60 bg-muted/30 px-2 py-0.5 text-xs"
            >
              {d}
              <button
                type="button"
                onClick={() => handleRemove(d)}
                aria-label={t("settings.domains.remove", { domain: d })}
                className="text-muted-foreground hover:text-destructive"
              >
                <X className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      )}

      <div className="flex items-center gap-2">
        <Input
          value={newDomain}
          onChange={(e) => setNewDomain(e.target.value)}
          placeholder={t("settings.domains.addPlaceholder")}
          className="h-9"
        />
        <Button type="button" variant="outline" size="sm" onClick={handleAdd}>
          <Plus className="mr-1 size-3.5" />
          {t("settings.domains.addButton")}
        </Button>
      </div>
      {addError && <p className="text-xs text-destructive">{addError}</p>}
    </div>
  );
}
