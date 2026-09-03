import { Loader2 } from "lucide-react";

import type { TrackerPreview } from "@/lib/api";
import { PosterImage } from "./PosterImage";

interface TopicPreviewCardProps {
  /** Resolved metadata, or null while nothing has arrived yet. */
  preview: TrackerPreview | null;
  /** True while the lookup is in flight and has produced nothing yet. */
  pending: boolean;
}

/**
 * One card, two states, for the AddTopic form's title/poster preview.
 *
 * Resolving a preview can take seconds — a login-gated tracker has to warm a
 * session before it can even read the page — and with nothing on screen the
 * form looked inert, so users retyped the URL or gave up. The skeleton
 * occupies the same box the result will, so nothing jumps when it arrives.
 *
 * Renders nothing at all when there is neither a lookup in flight nor
 * anything worth showing, so the caller can mount it unconditionally.
 */
export function TopicPreviewCard({ preview, pending }: TopicPreviewCardProps) {
  const hasContent = !!preview && (!!preview.title || !!preview.image_url);
  if (!pending && !hasContent) return null;

  return (
    <div className="flex items-center gap-3 rounded-md border border-border/60 bg-muted/30 p-3">
      {pending ? (
        <>
          <div className="h-16 w-12 shrink-0 animate-pulse rounded bg-muted" />
          <div className="min-w-0 flex-1 space-y-2">
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Loader2 className="size-3 animate-spin" />
              Resolving title and poster…
            </div>
            <div className="h-4 w-2/3 animate-pulse rounded bg-muted" />
          </div>
        </>
      ) : (
        <>
          {/* ghost: show a same-sized "no image" box rather than nothing. A
              silently absent poster and a poster that failed to load look
              identical when the element just disappears, which made a real
              bug hard to tell from a tracker that has no artwork. */}
          <PosterImage
            ghost
            src={preview!.image_url}
            alt={preview!.title || "preview"}
            className="h-16 w-12 shrink-0 rounded object-cover"
          />
          <div className="min-w-0">
            <div className="text-xs text-muted-foreground">Preview</div>
            <div className="truncate text-sm font-medium">{preview!.title || "—"}</div>
          </div>
        </>
      )}
    </div>
  );
}
