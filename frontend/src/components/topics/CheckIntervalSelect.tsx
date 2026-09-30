import { Label } from "@/components/ui/label";
import { useT } from "@/i18n";
import { CHECK_INTERVAL_PRESETS, formatCheckInterval } from "@/lib/check-interval";
import { SELECT_CLASS } from "./SeasonEpisodePicker";

interface CheckIntervalSelectProps {
  value: number;
  onChange: (sec: number) => void;
}

// Picks how often a topic is checked (issue #204). A stored value that is not
// a preset (set through the API) gets its own option, so opening the edit form
// never silently swaps it for the first preset.
export function CheckIntervalSelect({ value, onChange }: CheckIntervalSelectProps) {
  const t = useT();
  const options = CHECK_INTERVAL_PRESETS.includes(value)
    ? CHECK_INTERVAL_PRESETS
    : [...CHECK_INTERVAL_PRESETS, value].sort((a, b) => a - b);
  return (
    <div className="space-y-1.5">
      <Label htmlFor="check-interval">{t("topics.interval.label")}</Label>
      <select
        id="check-interval"
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className={SELECT_CLASS}
      >
        {options.map((sec) => (
          <option key={sec} value={sec}>
            {formatCheckInterval(sec, t)}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">{t("topics.interval.help")}</p>
    </div>
  );
}
