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

// NotifierBadge shows where a topic's release alerts go — mirroring ClientBadge,
// it always renders. A topic with an explicit NotifierID shows that notifier; a
// topic with none falls back to the user's default notifiers (the same
// resolution the scheduler performs at send time), shown muted.
export function NotifierBadge({ topic, notifierById }: NotifierBadgeProps) {
  const explicit = topic.NotifierID ? notifierById.get(topic.NotifierID) : undefined;

  // Explicit notifier picked on the topic.
  if (explicit) {
    return (
      <Badge variant="outline" className="shrink-0 gap-1 font-normal">
        <Bell className="size-3" />
        {explicit.display_name}
      </Badge>
    );
  }

  // NotifierID set but the notifier no longer exists (deleted after assignment).
  // The topics.notifier_id FK is ON DELETE SET NULL so this is defensive.
  if (topic.NotifierID) {
    return (
      <Badge variant="outline" className="shrink-0 gap-1 font-normal text-destructive">
        <Bell className="size-3" />
        unknown notifier
      </Badge>
    );
  }

  // No per-topic notifier → falls back to the user's default notifiers.
  return (
    <Badge variant="outline" className="shrink-0 gap-1 font-normal text-muted-foreground">
      <Bell className="size-3" />
      default notifiers
    </Badge>
  );
}
