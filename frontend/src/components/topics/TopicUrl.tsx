import { useState } from "react";
import { Copy, Check } from "lucide-react";

interface TopicUrlProps {
  url: string;
}

// Collapse a long magnet URI to its canonical infohash form, dropping the
// noisy &tr=…&dn=… query params. Falls back to the raw string if no btih
// infohash is present.
function magnetLabel(url: string): string {
  const match = url.match(/xt=urn:btih:([^&]+)/i);
  return match ? `magnet:?xt=urn:btih:${match[1]}` : url;
}

// TopicUrl renders a topic's source. HTTP(S) URLs render as-is (truncated by
// CSS). Magnet links are shown in a compact infohash-only form with a button
// to copy the full magnet URI to the clipboard.
export function TopicUrl({ url }: TopicUrlProps) {
  const [copied, setCopied] = useState(false);

  if (!url.startsWith("magnet:")) {
    return (
      <div className="truncate font-mono text-xs text-muted-foreground">{url}</div>
    );
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API is unavailable in insecure contexts — fail silently.
    }
  };

  return (
    <div className="flex items-center gap-1.5">
      <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
        {magnetLabel(url)}
      </span>
      <button
        type="button"
        onClick={copy}
        aria-label="Copy magnet link"
        title="Copy magnet link"
        className="shrink-0 text-muted-foreground/60 transition-colors hover:text-foreground"
      >
        {copied ? (
          <Check className="size-3 text-success" />
        ) : (
          <Copy className="size-3" />
        )}
      </button>
    </div>
  );
}
