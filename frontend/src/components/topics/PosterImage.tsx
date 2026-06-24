import { useState } from "react";
import { ImageOff } from "lucide-react";

import { cn } from "@/lib/utils";

interface PosterImageProps {
  src: string | null | undefined;
  alt: string;
  className?: string;
  // When true, a same-sized placeholder box is rendered instead of nothing
  // when the image is missing/failed — so rows in a list stay aligned. When
  // false/unset, the component renders null (honest absence, e.g. in the
  // single-card AddTopic preview where alignment doesn't matter).
  ghost?: boolean;
}

// Renders a tracker poster/preview image, hiding itself cleanly when the URL
// is empty or fails to load (tracker CDNs sometimes hotlink-block or require a
// referer). No fake poster is shown — with `ghost`, a neutral placeholder of
// the same size keeps list rows aligned without pretending an image exists.
export function PosterImage({ src, alt, className, ghost }: PosterImageProps) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) {
    if (!ghost) return null;
    return (
      <div
        aria-hidden="true"
        className={cn(
          className,
          "flex items-center justify-center bg-muted/40 ring-1 ring-inset ring-border/50",
        )}
      >
        <ImageOff className="size-4 text-muted-foreground/40" />
      </div>
    );
  }
  return (
    <img
      src={src}
      alt={alt}
      loading="lazy"
      onError={() => setFailed(true)}
      className={className}
    />
  );
}
