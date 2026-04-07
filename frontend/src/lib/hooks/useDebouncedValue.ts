import { useEffect, useState } from "react";

/**
 * Returns a debounced copy of `value` that only updates after `delayMs`
 * milliseconds of stability.
 *
 * Useful for feeding text-input state into React Query's `enabled`
 * flag without firing a request on every keystroke:
 *
 * ```tsx
 * const debounced = useDebouncedValue(url, 350);
 * useQuery({
 *   queryKey: QK.trackerMatch(debounced),
 *   queryFn: () => api.get(`/trackers/match?url=${debounced}`),
 *   enabled: debounced.length >= 8,
 * });
 * ```
 */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);

  useEffect(() => {
    const handle = window.setTimeout(() => setDebounced(value), delayMs);
    return () => window.clearTimeout(handle);
  }, [value, delayMs]);

  return debounced;
}
