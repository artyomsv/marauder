import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, Download } from "lucide-react";

import { api, type DeliveryStatus as Delivery, type TopicStatus } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
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
const ACTIVE_POLL_MS = 3000;
const IDLE_POLL_MS = 20000;

// Episodic labels are "sNNeNN" (LostFilm). Anything else (a RuTracker
// release name, etc.) is a single-torrent delivery shown ungrouped.
const SEASON_EP_RE = /^s(\d+)e(\d+)$/i;

type Kind = "downloading" | "finished" | "delivered";

function classify(d: Delivery): Kind {
  if (d.percent_done == null) return "delivered";
  if (d.percent_done >= 1 || d.state === "seeding") return "finished";
  return "downloading";
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
  const { data } = useQuery<TopicStatus>({
    queryKey: QK.topicStatus(topicId),
    queryFn: () => api.topicStatus(topicId),
    // Fast poll while a download is in flight, slow baseline otherwise so
    // new deliveries and finished-flips surface without a page refresh.
    refetchInterval: (query) => {
      const d = query.state.data;
      const active = d?.deliveries.some((x) => ACTIVE_STATES.has(x.state));
      return active ? ACTIVE_POLL_MS : IDLE_POLL_MS;
    },
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
        <div className="flex flex-wrap items-center gap-1">
          {loose.map((d) => (
            <DeliveryChip key={d.infohash} delivery={d} label={d.label} t={t} />
          ))}
        </div>
      )}
    </div>
  );
}

interface DeliveryChipProps {
  delivery: Delivery;
  label: string;
  t: (key: string, vars?: Record<string, string | number>) => string;
}

function DeliveryChip({ delivery, label, t }: DeliveryChipProps) {
  const kind = classify(delivery);
  const text = label || delivery.infohash.slice(0, 8);

  if (kind === "downloading") {
    const pct = Math.round((delivery.percent_done ?? 0) * 100);
    return (
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-xs",
          "border-primary/40 text-primary",
        )}
        title={t("topics.delivery.downloading")}
      >
        <Download className="size-3" />
        {text} {pct}%
      </span>
    );
  }

  if (kind === "finished") {
    return (
      <span
        className="inline-flex items-center gap-1 rounded border border-emerald-500/40 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400"
        title={t("topics.delivery.finished")}
      >
        <CheckCircle2 className="size-3" />
        {text}
      </span>
    );
  }

  return (
    <span
      className="inline-flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-xs text-muted-foreground"
      title={t("topics.delivery.delivered")}
    >
      {text}
    </span>
  );
}
