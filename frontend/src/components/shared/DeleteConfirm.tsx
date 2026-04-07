import { useEffect, useRef, useState } from "react";
import { Check, Loader2, Trash2, X } from "lucide-react";

import { Button } from "@/components/ui/button";

interface Props {
  /** Called once the user clicks the inner "yes" button. */
  onConfirm: () => void;
  /** Optional flag to show a spinner inside the trash icon while a
   *  delete request is in flight. */
  isPending?: boolean;
  /** Auto-cancel after this many seconds if the user neither confirms
   *  nor cancels. Defaults to 4. */
  timeoutSeconds?: number;
  /** Accessible label for the initial trash button. */
  label?: string;
}

/**
 * DeleteConfirm renders a single trash icon button. On first click it
 * swaps in place to a small inline confirmation row ("Delete? ✓ ✗")
 * — no modal dialog. The second click on ✓ fires `onConfirm`. ✗ or a
 * 4-second timeout cancels.
 *
 * Use it anywhere a row needs a destructive action with a quick
 * "are you sure?" without breaking visual flow:
 *
 *   <DeleteConfirm
 *     onConfirm={() => del.mutate(item.id)}
 *     isPending={del.isPending && del.variables === item.id}
 *     label="Delete topic"
 *   />
 */
export function DeleteConfirm({
  onConfirm,
  isPending = false,
  timeoutSeconds = 4,
  label = "Delete",
}: Props) {
  const [armed, setArmed] = useState(false);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    if (!armed) return;
    timer.current = window.setTimeout(() => setArmed(false), timeoutSeconds * 1000);
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current);
    };
  }, [armed, timeoutSeconds]);

  if (!armed) {
    return (
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="text-destructive hover:text-destructive"
        onClick={(e) => {
          e.stopPropagation();
          setArmed(true);
        }}
        aria-label={label}
        disabled={isPending}
      >
        {isPending ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <Trash2 className="size-4" />
        )}
      </Button>
    );
  }

  return (
    <div
      role="group"
      aria-label="Confirm delete"
      className="inline-flex items-center gap-0.5 rounded-md border border-destructive/40 bg-destructive/10 px-1 py-0.5"
    >
      <span className="px-1 text-[11px] font-medium text-destructive">Delete?</span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 gap-1 px-1.5 text-destructive hover:bg-destructive/15 hover:text-destructive"
        onClick={(e) => {
          e.stopPropagation();
          setArmed(false);
          onConfirm();
        }}
        aria-label="Confirm delete"
      >
        <Check className="size-3.5" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-7 gap-1 px-1.5 text-muted-foreground hover:text-foreground"
        onClick={(e) => {
          e.stopPropagation();
          setArmed(false);
        }}
        aria-label="Cancel delete"
      >
        <X className="size-3.5" />
      </Button>
    </div>
  );
}
