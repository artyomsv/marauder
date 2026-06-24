import { Tv } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { Topic } from "@/lib/api";

/**
 * Shows a "Sonarr" badge for topics auto-imported by the Sonarr integration
 * (marked with extra.source === "sonarr"). Renders nothing for manually-added
 * topics. Mirrors ClientBadge / NotifierBadge.
 */
export function SonarrBadge({ topic }: { topic: Topic }) {
  if (topic.Extra?.source !== "sonarr") return null;
  return (
    <Badge variant="secondary" className="shrink-0 gap-1 font-normal">
      <Tv className="size-3" />
      Sonarr
    </Badge>
  );
}
