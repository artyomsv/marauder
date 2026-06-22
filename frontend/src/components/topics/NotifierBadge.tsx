import { Bell } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { Topic } from "@/lib/api";

// Minimal notifier shape needed to render the badge — id → display name.
export interface NotifierRef {
  id: string;
  display_name: string;
}

interface NotifierBadgeProps {
  topic: Topic;
  notifierById: Map<string, NotifierRef>;
}

// NotifierBadge shows which single notifier a topic's release alerts are
// routed to, when the topic overrides the default. Topics without an
// override use the global notifier fan-out (the default for every install),
// so nothing is rendered for them to keep the card uncluttered.
export function NotifierBadge({ topic, notifierById }: NotifierBadgeProps) {
  if (!topic.NotifierID) return null;
  const notifier = notifierById.get(topic.NotifierID);
  if (!notifier) return null;

  return (
    <Badge variant="outline" className="shrink-0 gap-1 font-normal text-muted-foreground">
      <Bell className="size-3" />
      {notifier.display_name}
    </Badge>
  );
}
