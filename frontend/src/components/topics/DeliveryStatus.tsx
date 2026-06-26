import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, CheckCircle2, Copy, Download, Info } from "lucide-react";

import { api, type DeliveryStatus as Delivery, type TopicStatus } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { useSseStatus } from "@/lib/sse-status";
import { cn } from "@/lib/utils";

interface DeliveryStatusProps {
  topicId: string;
}

// States that are still actively changing — while any delivery is in one of
// these, the row polls fast for live progress.
const ACTIVE_STATES = new Set(["downloading", "checking", "queued"]);

// Poll cadence. Fast while something is actively downloading; a slow
// baseline otherwise so a freshly-added topic's first delivery (and a
// settled torrent flipping to "finished") appears without a manual page
// refresh. The baseline is deliberately gentle for self-hosted setups.
export const ACTIVE_POLL_MS = 3000;
export const IDLE_POLL_MS = 20000;

// pollInterval decides the /status refetch cadence. While SSE is connected,
// live updates arrive via setQueryData, so polling is disabled; otherwise it
// falls back to fast-while-active / slow-baseline.
export function pollInterval(
  sseConnected: boolean,
  deliveries: { state: string }[] | undefined,
): number | false {
  if (sseConnected) return false;
  const active = deliveries?.some((x) => ACTIVE_STATES.has(x.state));
  return active ? ACTIVE_POLL_MS : IDLE_POLL_MS;
}

// Episodic labels are "sNNeNN" (LostFilm). Anything else (a RuTracker
// release name, etc.) is a single-torrent delivery shown ungrouped.
const SEASON_EP_RE = /^s(\d+)e(\d+)$/i;

type Kind = "downloading" | "finished" | "delivered";

function classify(d: Delivery): Kind {
  if (d.percent_done == null) return "delivered";
  if (d.percent_done >= 1 || d.state === "seeding") return "finished";
  return "downloading";
}

// Short, locale-aware date for delivery timestamps (e.g. "Jun 26").
function fmtDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// Newest delivered_at among a set (ISO-8601 strings sort lexicographically).
function newestDeliveredAt(ds: Delivery[]): string {
  if (ds.length === 0) return "";
  return ds.reduce((a, b) => (a.delivered_at > b.delivered_at ? a : b)).delivered_at;
}

interface SeasonGroup {
  season: number;
  items: { delivery: Delivery; episode: number }[];
}

// group splits deliveries into per-season episode groups (sorted by season,
// then episode ascending) plus a flat list of non-episodic releases.
function group(deliveries: Delivery[]): { seasons: SeasonGroup[]; loose: Delivery[] } {
  const bySeason = new Map<number, { delivery: Delivery; episode: number }[]>();
  const loose: Delivery[] = [];
  for (const d of deliveries) {
    const m = SEASON_EP_RE.exec(d.label.trim());
    if (!m) {
      loose.push(d);
      continue;
    }
    const season = parseInt(m[1], 10);
    const episode = parseInt(m[2], 10);
    const bucket = bySeason.get(season) ?? [];
    bucket.push({ delivery: d, episode });
    bySeason.set(season, bucket);
  }
  const seasons = [...bySeason.entries()]
    .map(([season, items]) => ({
      season,
      items: items.sort((a, b) => a.episode - b.episode),
    }))
    .sort((a, b) => a.season - b.season);
  return { seasons, loose };
}

// DeliveryStatus shows what a topic has pushed to its client. For series it
// groups episodes by season (sorted by episode); single-torrent topics show
// a flat release list. Tier 1 (always) is the labels; Tier 2 (when the
// client supports status) augments in-progress items with a live percentage
// and a "finished" mark. Renders nothing until there is a delivery, so
// untouched topics stay visually quiet.
export function DeliveryStatus({ topicId }: DeliveryStatusProps) {
  const t = useT();
  const sseConnected = useSseStatus((s) => s.connected);
  const { data } = useQuery<TopicStatus>({
    queryKey: QK.topicStatus(topicId),
    queryFn: () => api.topicStatus(topicId),
    // Fast poll while a download is in flight, slow baseline otherwise so
    // new deliveries and finished-flips surface without a page refresh.
    // SSE takes precedence — when connected, polling is disabled as live
    // updates arrive via setQueryData.
    refetchInterval: (query) => pollInterval(sseConnected, query.state.data?.deliveries),
  });

  const deliveries = data?.deliveries ?? [];
  if (deliveries.length === 0) return null;

  const { seasons, loose } = group(deliveries);

  return (
    <div className="mt-1.5 flex flex-col gap-1">
      <span className="text-xs text-muted-foreground">
        {t("topics.delivery.count", { n: deliveries.length })}
      </span>
      {seasons.map((g) => {
        const done = g.items.filter((it) => classify(it.delivery) !== "downloading").length;
        return (
          <div key={g.season} className="flex flex-wrap items-center gap-1">
            <span className="text-xs font-medium text-muted-foreground">
              {t("topics.delivery.season", { n: g.season })} · {done}/{g.items.length}
            </span>
            {g.items.map(({ delivery, episode }) => (
              <DeliveryChip
                key={delivery.infohash}
                delivery={delivery}
                label={`E${String(episode).padStart(2, "0")}`}
                t={t}
              />
            ))}
          </div>
        );
      })}
      {loose.length > 0 && (
        <div className="flex flex-col gap-1">
          {loose.length >= 2 && (
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
              <Info className="size-3 shrink-0" />
              {t("topics.delivery.reissued", {
                n: loose.length,
                date: fmtDate(newestDeliveredAt(loose)),
              })}
            </span>
          )}
          <div className="flex flex-wrap items-center gap-1">
            {loose.map((d) => (
              <DeliveryChip key={d.infohash} delivery={d} label={d.label} copyable t={t} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

interface DeliveryChipProps {
  delivery: Delivery;
  label: string;
  // copyable marks a single-torrent release chip: its (potentially long)
  // label truncates, the tooltip shows the full label + delivered date, and a
  // copy button lifts the full label to the clipboard. Episode chips (short)
  // leave it false.
  copyable?: boolean;
  t: (key: string, vars?: Record<string, string | number>) => string;
}

const CHIP_STYLE: Record<Kind, string> = {
  downloading: "border-primary/40 text-primary",
  finished: "border-emerald-500/40 text-emerald-600 dark:text-emerald-400",
  delivered: "border-border text-muted-foreground",
};

function DeliveryChip({ delivery, label, copyable, t }: DeliveryChipProps) {
  const [copied, setCopied] = useState(false);
  const kind = classify(delivery);
  const text = label || delivery.infohash.slice(0, 8);
  const stateTitle = t(`topics.delivery.${kind}`);
  // A copyable chip's tooltip is the full label + when it was delivered; a
  // plain chip's tooltip is just its state.
  const title = copyable
    ? `${text} · ${t("topics.delivery.deliveredOn", { date: fmtDate(delivery.delivered_at) })}`
    : stateTitle;

  const handleCopy = async () => {
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure context / denied) — leave the chip as-is
    }
  };

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-xs",
        CHIP_STYLE[kind],
      )}
      title={title}
    >
      {kind === "downloading" && <Download className="size-3 shrink-0" />}
      {kind === "finished" && <CheckCircle2 className="size-3 shrink-0" />}
      <span className={cn(copyable && "max-w-[16rem] truncate")}>{text}</span>
      {kind === "downloading" && (
        <span className="shrink-0">{Math.round((delivery.percent_done ?? 0) * 100)}%</span>
      )}
      {copyable && (
        <button
          type="button"
          onClick={handleCopy}
          title={copied ? t("topics.delivery.copied") : t("topics.delivery.copy")}
          aria-label={t("topics.delivery.copy")}
          className="shrink-0 opacity-60 transition-opacity hover:opacity-100"
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
        </button>
      )}
    </span>
  );
}
