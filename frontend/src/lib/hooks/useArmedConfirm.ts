import { useEffect, useRef, useState } from "react";

interface UseArmedConfirmOptions {
  /** Auto-disarm after this many milliseconds. Default: 4000 (4 seconds). */
  timeoutMs?: number;
}

interface UseArmedConfirmResult {
  /** True after `arm()` was called and before `disarm()` / timeout. */
  armed: boolean;
  /** Transition to the armed state. Resets the auto-disarm timer. */
  arm: () => void;
  /** Transition back to idle, cancelling the timer. */
  disarm: () => void;
  /** Convenience: disarm + run the confirm callback in one call. */
  confirmAndDisarm: (callback: () => void) => void;
}

/**
 * State machine for two-click "are you sure?" interactions: idle ⇄ armed,
 * with an auto-disarm timer that fires after `timeoutMs` of inactivity.
 *
 * Used by both `DeleteConfirm` (single-row trash → confirm) and the
 * Topics `BulkActionBar` (multi-select bulk delete → confirm).
 */
export function useArmedConfirm({ timeoutMs = 4000 }: UseArmedConfirmOptions = {}): UseArmedConfirmResult {
  const [armed, setArmed] = useState(false);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    if (!armed) return;
    timerRef.current = window.setTimeout(() => setArmed(false), timeoutMs);
    return () => {
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [armed, timeoutMs]);

  return {
    armed,
    arm: () => setArmed(true),
    disarm: () => setArmed(false),
    confirmAndDisarm: (cb) => {
      setArmed(false);
      cb();
    },
  };
}
