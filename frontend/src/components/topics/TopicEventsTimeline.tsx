import { useQuery } from "@tanstack/react-query";
import { Loader2, Clock } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { EVENT_LABELS } from "@/lib/events";

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
  return (
    <ol className="space-y-2 p-2">
      {events.map((e) => (
        <li key={e.id} className="flex items-start gap-3 text-sm">
          <span className={`mt-1.5 size-2 shrink-0 rounded-full ${severityDot[e.severity] ?? "bg-muted"}`} />
          <div className="min-w-0">
            <div className="font-medium">
              {EVENT_LABELS[e.event_type] ? t(EVENT_LABELS[e.event_type]) : e.event_type}
            </div>
            {/* The persisted message is the event's Title, which for every
                topic-scoped event is (a variant of) the topic's own display
                name — pure duplication inside a per-topic timeline, so it is
                deliberately not rendered. Error details have their own home
                (TopicError / last_error_code). */}
            <time className="text-xs text-muted-foreground">
              {new Date(e.created_at).toLocaleString()}
            </time>
          </div>
        </li>
      ))}
    </ol>
  );
}
