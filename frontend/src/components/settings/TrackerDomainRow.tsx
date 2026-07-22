import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ChevronRight, Globe, Loader2, Plus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SELECT_CLASS } from "@/components/topics/SeasonEpisodePicker";
import { api, type TrackerDomains } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

// Lowercased-trim hostname validation: RFC-1123-ish labels, at least two of
// them, no scheme/port/path. Mirrors the backend's validateHostname so a
// client-side reject never fires a request the server would 422 anyway.
const HOSTNAME_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;

interface Props {
  tracker: TrackerDomains;
}

interface InlineMsg {
  kind: "ok" | "err";
  text: string;
}

// One tracker's collapsible row: collapsed it shows the live domain as a pill;
// expanded it reveals the active-domain select, mirror management, and a Test
// button. Keeping most trackers collapsed turns a wall of controls into a
// scannable list (issue #126).
export function TrackerDomainRow({ tracker }: Props) {
  const t = useT();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [addError, setAddError] = useState<string | null>(null);
  const [testMsg, setTestMsg] = useState<InlineMsg | null>(null);
  const [saveMsg, setSaveMsg] = useState<InlineMsg | null>(null);

  const updateMut = useMutation({
    mutationFn: (body: { active_domain: string; custom_domains: string[] }) =>
      api.updateTrackerDomains(tracker.name, body),
    onSuccess: () => {
      setSaveMsg(null);
      qc.invalidateQueries({ queryKey: QK.trackerDomains });
    },
    onError: () => setSaveMsg({ kind: "err", text: t("settings.domains.saveFailed") }),
  });

  const testMut = useMutation({
    mutationFn: (domain: string) => api.testTrackerDomain(tracker.name, domain),
    onSuccess: (r) =>
      setTestMsg(
        r.ok
          ? { kind: "ok", text: t("settings.domains.testOk") }
          : // Surface the backend's reason (e.g. "empty page", "HTTP 403") so a
            // stub mirror that answers but serves nothing is no longer reported
            // as merely "unreachable".
            { kind: "err", text: r.detail || t("settings.domains.testFail") },
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
    if (tracker.custom_domains.includes(normalized) || tracker.known_domains.includes(normalized)) {
      setAddError(t("settings.domains.duplicateHostname"));
      return;
    }
    // Clear the input only once the save succeeds, so a failed save keeps the
    // user's typed value.
    updateMut.mutate(
      {
        active_domain: tracker.active_domain,
        custom_domains: [...tracker.custom_domains, normalized],
      },
      { onSuccess: () => setNewDomain("") },
    );
  };

  const handleRemove = (domain: string) => {
    updateMut.mutate({
      // Fall back to the plugin default if the removed domain was active.
      active_domain: tracker.active_domain === domain ? "" : tracker.active_domain,
      custom_domains: tracker.custom_domains.filter((d) => d !== domain),
    });
  };

  // Tests the mirror the user is about to add (if they've typed one) so it can
  // be verified before saving; otherwise falls back to the selected/saved domain.
  const handleTest = () => {
    setTestMsg(null);
    const candidate = newDomain.trim().toLowerCase();
    if (candidate) {
      if (!HOSTNAME_RE.test(candidate)) {
        setTestMsg({ kind: "err", text: t("settings.domains.invalidHostname") });
        return;
      }
      testMut.mutate(candidate);
      return;
    }
    testMut.mutate(tracker.active_domain || tracker.default_domain);
  };

  const currentDomain = tracker.active_domain || tracker.default_domain;
  const overridden = tracker.active_domain !== "" && tracker.active_domain !== tracker.default_domain;
  // known_domains includes the default domain itself — drop it here so it isn't
  // offered twice (once via the "(default)" option, once bare).
  const alternatives = tracker.known_domains.filter((d) => d !== tracker.default_domain);
  const singleDomain = tracker.known_domains.length === 1 && tracker.custom_domains.length === 0;
  const selectId = `tracker-domain-select-${tracker.name}`;
  const addInputId = `tracker-domain-add-${tracker.name}`;

  return (
    <div
      data-testid={`domain-row-${tracker.name}`}
      className="border-b border-border/50 last:border-b-0"
    >
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-3 rounded-md px-2 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span
          className={cn(
            "size-2 shrink-0 rounded-full ring-4",
            overridden
              ? "bg-primary ring-primary/20"
              : "bg-muted-foreground/50 ring-muted-foreground/10",
          )}
        />
        <span className="min-w-0">
          <span className="block text-sm font-medium">{tracker.display_name}</span>
          <span className="block text-xs text-muted-foreground">
            {overridden ? t("settings.domains.overridden") : t("settings.domains.usingDefault")}
          </span>
        </span>
        <span className="ml-auto flex items-center gap-2">
          <span
            className={cn(
              "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-xs",
              overridden
                ? "border-primary/40 bg-primary/10 text-primary"
                : "border-border bg-muted/40 text-foreground",
            )}
          >
            <Globe className="size-3" />
            {currentDomain}
          </span>
          <ChevronRight
            className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-90")}
          />
        </span>
      </button>

      {open && (
        <div className="grid gap-5 px-2 pb-5 pl-7 sm:grid-cols-[minmax(0,15rem)_1fr]">
          <div>
            <label
              htmlFor={selectId}
              className="mb-1.5 block font-mono text-[10px] uppercase tracking-wider text-muted-foreground"
            >
              {t("settings.domains.activeLabel")}
            </label>
            <select
              id={selectId}
              value={tracker.active_domain}
              onChange={handleSelect}
              className={cn(SELECT_CLASS, "font-mono disabled:cursor-not-allowed disabled:opacity-50")}
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
            {singleDomain && (
              <p className="mt-2 text-xs text-muted-foreground">
                {t("settings.domains.singleDomainHint")}
              </p>
            )}
            {saveMsg && <p className="mt-2 text-xs text-destructive">{saveMsg.text}</p>}
          </div>

          <div>
            <p className="mb-1.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
              {t("settings.domains.mirrorsLabel")}
            </p>
            <div className="mb-3 flex flex-wrap gap-1.5">
              {tracker.known_domains.map((d) => (
                <span
                  key={d}
                  className={cn(
                    "rounded-md border px-2 py-0.5 font-mono text-[11px]",
                    d === currentDomain
                      ? "border-primary/50 bg-primary/10 text-primary"
                      : "border-border bg-muted/30 text-foreground",
                  )}
                >
                  {d}
                </span>
              ))}
              {tracker.custom_domains.map((d) => (
                <span
                  key={d}
                  className="inline-flex items-center gap-1 rounded-md border border-warning/40 bg-warning/10 px-2 py-0.5 font-mono text-[11px] text-warning"
                >
                  {d}
                  <button
                    type="button"
                    onClick={() => handleRemove(d)}
                    disabled={updateMut.isPending}
                    aria-label={t("settings.domains.remove", { domain: d })}
                    className="opacity-70 hover:text-destructive hover:opacity-100 disabled:opacity-40"
                  >
                    <X className="size-3" />
                  </button>
                </span>
              ))}
            </div>
            <div className="flex items-center gap-2">
              <Input
                id={addInputId}
                value={newDomain}
                onChange={(e) => setNewDomain(e.target.value)}
                placeholder={t("settings.domains.addPlaceholder")}
                aria-label={t("settings.domains.addLabel")}
                className="h-9 font-mono"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleAdd}
                disabled={updateMut.isPending}
              >
                <Plus className="mr-1 size-3.5" />
                {t("settings.domains.addButton")}
              </Button>
            </div>
            {addError && <p className="mt-1.5 text-xs text-destructive">{addError}</p>}
          </div>

          <div className="flex items-center justify-end gap-3 sm:col-span-2">
            {testMsg && (
              <span
                className={cn(
                  "text-xs",
                  testMsg.kind === "ok" ? "text-success" : "text-destructive",
                )}
              >
                {testMsg.text}
              </span>
            )}
            <Button variant="outline" size="sm" onClick={handleTest} disabled={testMut.isPending}>
              {testMut.isPending ? (
                <Loader2 className="mr-1.5 size-3.5 animate-spin" />
              ) : (
                <CheckCircle2 className="mr-1.5 size-3.5" />
              )}
              {t("settings.domains.test")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
