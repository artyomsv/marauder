import { Timer } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { useT } from "@/i18n";
import type { Topic } from "@/lib/api";
import { formatCheckInterval } from "@/lib/check-interval";

/**
 * Shows how often a topic is checked (issue #204). Mirrors NotifyOnlyBadge.
 */
export function CheckIntervalBadge({ topic }: { topic: Topic }) {
  const t = useT();
  if (!topic.CheckIntervalSec) return null;
  const label = formatCheckInterval(topic.CheckIntervalSec, t);
  return (
    <Badge
      variant="secondary"
      className="shrink-0 gap-1 font-normal"
      title={t("topics.interval.badgeTitle", { interval: label })}
    >
      <Timer className="size-3" />
      {label}
    </Badge>
  );
}
