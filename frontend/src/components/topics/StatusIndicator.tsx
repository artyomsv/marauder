import type { Topic } from "@/lib/api";

export function StatusIndicator({ status }: { status: Topic["Status"] }) {
  const cls =
    status === "active"
      ? "bg-success"
      : status === "error"
      ? "bg-destructive"
      : "bg-muted-foreground";
  // Only an errored topic pulses. A never-ending animation on every row costs
  // one compositor layer per topic for as long as the page is open, which on a
  // long list made scrolling stutter (issue #201) — and a pulse on every
  // healthy row drew the eye to nothing.
  return (
    <span className="relative flex size-2.5">
      {status === "error" && (
        <span
          className={`absolute inline-flex h-full w-full animate-ping rounded-full ${cls} opacity-40`}
        />
      )}
      <span className={`relative inline-flex size-2.5 rounded-full ${cls}`} />
    </span>
  );
}
