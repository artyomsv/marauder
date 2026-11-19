import { Label } from "@/components/ui/label";
import { useT } from "@/i18n";
import { CHECK_INTERVAL_PRESETS, formatCheckInterval } from "@/lib/check-interval";
import { SELECT_CLASS } from "./SeasonEpisodePicker";

interface CheckIntervalSelectProps {
  value: number;
  onChange: (sec: number) => void;
  // The Settings page reuses this for the instance default (issue #250), so
  // the id and help text can be overridden there.
  id?: string;
  help?: string;
  disabled?: boolean;
}

// Picks how often a topic is checked (issue #204). A stored value that is not
// a preset (set through the API) gets its own option, so opening the edit form
// never silently swaps it for the first preset.
export function CheckIntervalSelect({
  value,
  onChange,
  id = "check-interval",
  help,
  disabled,
}: CheckIntervalSelectProps) {
  const t = useT();
  const options = CHECK_INTERVAL_PRESETS.includes(value)
    ? CHECK_INTERVAL_PRESETS
    : [...CHECK_INTERVAL_PRESETS, value].sort((a, b) => a - b);
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{t("topics.interval.label")}</Label>
      <select
        id={id}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        className={SELECT_CLASS}
      >
        {options.map((sec) => (
          <option key={sec} value={sec}>
            {formatCheckInterval(sec, t)}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">{help ?? t("topics.interval.help")}</p>
    </div>
  );
}
