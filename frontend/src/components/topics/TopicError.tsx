import { AlertTriangle } from "lucide-react";

import type { Topic } from "@/lib/api";
import { useT } from "@/i18n";

// Maps the backend's stable last_error_code values to i18n keys. Keep in sync
// with the errCode* constants in backend/internal/scheduler/scheduler.go.
// "unknown" (and any unmapped code) intentionally has no key so we fall back
// to the raw LastError detail rather than a vague generic phrase.
const CODE_KEYS: Record<string, string> = {
  timeout: "topics.error.timeout",
  unreachable: "topics.error.unreachable",
  auth: "topics.error.auth",
  cloudflare: "topics.error.cloudflare",
  solver: "topics.error.solver",
  parse: "topics.error.parse",
  plugin_missing: "topics.error.pluginMissing",
  client: "topics.error.client",
  internal: "topics.error.internal",
};

interface TopicErrorProps {
  topic: Topic;
}

/**
 * Renders a topic's last check error as a localised, user-friendly line.
 * The raw backend error is preserved in the `title` tooltip for debugging,
 * and is shown verbatim when the code is unknown (or absent on older rows).
 */
export function TopicError({ topic }: TopicErrorProps) {
  const t = useT();
  if (!topic.LastError) return null;

  const key = CODE_KEYS[topic.LastErrorCode];
  const message = key ? t(key) : topic.LastError;

  return (
    <div
      className="mt-1 flex items-center gap-1.5 text-xs text-destructive"
      title={topic.LastError}
    >
      <AlertTriangle className="size-3" />
      {message}
    </div>
  );
}
