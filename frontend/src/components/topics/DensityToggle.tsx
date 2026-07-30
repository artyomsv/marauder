import { Rows3, Rows4 } from "lucide-react";

import { cn } from "@/lib/utils";

export function DensityToggle({
  density,
  setDensity,
}: {
  density: "comfortable" | "compact";
  setDensity: (d: "comfortable" | "compact") => void;
}) {
  return (
    <div className="inline-flex rounded-md border border-border/60 bg-background/40 p-0.5">
      <button
        type="button"
        aria-label="Comfortable density"
        onClick={() => setDensity("comfortable")}
        className={cn(
          "flex size-8 items-center justify-center rounded-sm transition-colors",
          density === "comfortable"
            ? "bg-primary/15 text-primary"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        <Rows3 className="size-4" />
      </button>
      <button
        type="button"
        aria-label="Compact density"
        onClick={() => setDensity("compact")}
        className={cn(
          "flex size-8 items-center justify-center rounded-sm transition-colors",
          density === "compact"
            ? "bg-primary/15 text-primary"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        <Rows4 className="size-4" />
      </button>
    </div>
  );
}
