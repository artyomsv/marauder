import { useState } from "react";

interface PosterImageProps {
  src: string | null | undefined;
  alt: string;
  className?: string;
}

// Renders a tracker poster/preview image, hiding itself cleanly when the URL
// is empty or fails to load (tracker CDNs sometimes hotlink-block or require a
// referer). No placeholder image is shown — absence stays honest about missing
// data rather than faking a poster.
export function PosterImage({ src, alt, className }: PosterImageProps) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) return null;
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
