import { useQuery } from "@tanstack/react-query";
import { Loader2, Clock } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { EVENT_LABELS, groupConsecutiveEvents } from "@/lib/events";

interface TopicEventsTimelineProps {
  topicId: string;
}

const severityDot: Record<string, string> = {
  info: "bg-primary",
  warn: "bg-warning",
  error: "bg-destructive",
};

export function TopicEventsTimeline({ topicId }: TopicEventsTimelineProps) {
  const t = useT();
  const { data, isLoading } = useQuery({
    queryKey: QK.topicEvents(topicId),
    queryFn: () => api.topicEvents(topicId),
  });
  const events = data?.events ?? [];

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        {t("common.loading")}
      </div>
    );
  }
  if (events.length === 0) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Clock className="size-4" />
        {t("topics.history.empty")}
      </div>
    );
  }
  // Runs of the same event type collapse into one row. A topic whose download
  // keeps failing re-detects the same release every retry tick, so the raw
  // feed is mostly duplicates that bury the rows which actually differ.
  const groups = groupConsecutiveEvents(events);

  return (
    <ol className="space-y-2 p-2">
      {groups.map((g) => (
        <li key={g.id} className="flex items-start gap-3 text-sm">
          <span className={`mt-1.5 size-2 shrink-0 rounded-full ${severityDot[g.severity] ?? "bg-muted"}`} />
          <div className="min-w-0">
            <div className="flex items-center gap-2 font-medium">
              {EVENT_LABELS[g.event_type] ? t(EVENT_LABELS[g.event_type]) : g.event_type}
              {g.count > 1 && (
                <span
                  className="rounded-full bg-muted px-1.5 py-0.5 text-xs font-normal text-muted-foreground"
                  aria-label={t("topics.history.repeated", { count: g.count })}
                >
                  ×{g.count}
                </span>
              )}
            </div>
            {/* The persisted message is the event's Title, which for every
                topic-scoped event is (a variant of) the topic's own display
                name — pure duplication inside a per-topic timeline, so it is
                deliberately not rendered. Error details have their own home
                (TopicError / last_error_code). */}
            <time className="text-xs text-muted-foreground" dateTime={g.newestAt}>
              {new Date(g.newestAt).toLocaleString()}
              {g.count > 1 && ` – ${new Date(g.oldestAt).toLocaleString()}`}
            </time>
          </div>
        </li>
      ))}
    </ol>
  );
}
