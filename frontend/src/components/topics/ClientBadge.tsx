import { Server } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import type { Topic } from "@/lib/api";

// Minimal client shape needed to render the badge. The full client view
// lives in the Clients page; here we only resolve id → display name and
// which client is the default.
export interface ClientRef {
  id: string;
  display_name: string;
  is_default: boolean;
}

interface ClientBadgeProps {
  topic: Topic;
  clientById: Map<string, ClientRef>;
  defaultClient: ClientRef | null;
}

// ClientBadge shows which torrent client a topic will deliver to. A topic
// with an explicit ClientID shows that client; a topic with no client
// (ClientID === null) falls back to the workspace default client — the
// same resolution the scheduler performs at delivery time.
export function ClientBadge({ topic, clientById, defaultClient }: ClientBadgeProps) {
  const explicit = topic.ClientID ? clientById.get(topic.ClientID) : undefined;

  // Explicit client picked on the topic.
  if (explicit) {
    return (
      <Badge variant="success" className="shrink-0 gap-1 font-normal">
        <Server className="size-3" />
        {explicit.display_name}
      </Badge>
    );
  }

  // ClientID set but the client no longer exists (deleted after assignment).
  if (topic.ClientID) {
    return (
      <Badge variant="destructive" className="shrink-0 gap-1 font-normal">
        <Server className="size-3" />
        unknown client
      </Badge>
    );
  }

  // No per-topic client → falls back to the default (green like an explicit
  // client; a missing default is an error state shown in red).
  return (
    <Badge
      variant={defaultClient ? "success" : "destructive"}
      className="shrink-0 gap-1 font-normal"
    >
      <Server className="size-3" />
      {defaultClient ? `${defaultClient.display_name} · default` : "no default client"}
    </Badge>
  );
}
