import { BellRing } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { useT } from "@/i18n";
import type { Topic } from "@/lib/api";

/**
 * Shows a "Notify only" badge for watch-only topics (issue #184) — monitored
 * and announced, never delivered to a torrent client. Renders nothing for
 * ordinary topics. Mirrors SonarrBadge / ClientBadge.
 */
export function NotifyOnlyBadge({ topic }: { topic: Topic }) {
  const t = useT();
  if (!topic.NotifyOnly) return null;
  return (
    <Badge variant="secondary" className="shrink-0 gap-1 font-normal">
      <BellRing className="size-3" />
      {t("topics.notifyOnly.badge")}
    </Badge>
  );
}
