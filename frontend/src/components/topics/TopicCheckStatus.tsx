import { Loader2, AlertCircle, Clock } from "lucide-react";

import { useCheckStatus } from "@/lib/check-status";
import { useT } from "@/i18n";

interface TopicCheckStatusProps {
  topicId: string;
}

// TopicCheckStatus shows the live check state for a topic, fed by check.*
// SSE events: a "checking…" pulse, an error chip, or a next-check countdown.
// Renders nothing until an event has arrived for this topic.
export function TopicCheckStatus({ topicId }: TopicCheckStatusProps) {
  const t = useT();
  const entry = useCheckStatus((s) => s.byTopic[topicId]);
  if (!entry) return null;

  if (entry.phase === "checking") {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-primary" title={t("topics.check.checking")}>
        <Loader2 className="size-3 animate-spin" />
        {t("topics.check.checking")}
      </span>
    );
  }
  if (entry.phase === "error") {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-destructive" title={entry.error ?? t("topics.check.error")}>
        <AlertCircle className="size-3" />
        {t("topics.check.error")}
      </span>
    );
  }
  if (entry.nextCheckAt) {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground" title={new Date(entry.nextCheckAt).toLocaleString()}>
        <Clock className="size-3" />
        {t("topics.check.next", { time: new Date(entry.nextCheckAt).toLocaleTimeString() })}
      </span>
    );
  }
  return null;
}
