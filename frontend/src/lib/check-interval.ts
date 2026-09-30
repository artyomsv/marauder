// Per-topic check interval (issue #204). The backend accepts 300..604800
// seconds (topics.MinCheckIntervalSec / MaxCheckIntervalSec) and uses 900 when
// a new topic does not set one.
export const DEFAULT_CHECK_INTERVAL_SEC = 900;

export const CHECK_INTERVAL_PRESETS: readonly number[] = [
  900, 1800, 3600, 10800, 21600, 43200, 86400, 259200, 604800,
];

export type IntervalUnit = "seconds" | "minutes" | "hours" | "days";

export interface IntervalParts {
  unit: IntervalUnit;
  count: number;
}

const MINUTE = 60;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// Splits an interval into the largest whole unit that describes it exactly.
// One day stays "24 h" — it reads better next to 6 h and 12 h, and it keeps
// the day label plural in every locale.
export function intervalParts(sec: number): IntervalParts {
  if (sec >= 2 * DAY && sec % DAY === 0) return { unit: "days", count: sec / DAY };
  if (sec >= HOUR && sec % HOUR === 0) return { unit: "hours", count: sec / HOUR };
  if (sec % MINUTE === 0) return { unit: "minutes", count: sec / MINUTE };
  return { unit: "seconds", count: sec };
}

// Localised short label, e.g. "15 min" / "6 h" / "3 days".
export function formatCheckInterval(
  sec: number,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  const { unit, count } = intervalParts(sec);
  return t(`topics.interval.${unit}`, { n: count });
}
