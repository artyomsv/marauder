import { useT } from "@/i18n";
import { NOTIFIABLE_EVENTS, EVENT_GROUP_EVENTS, EVENT_LABELS } from "@/lib/events";

interface EventPickerProps {
  value: string[];
  onChange: (next: string[]) => void;
}

// expandStored turns a stored subscription (which may contain the legacy
// "updated"/"error" keywords) into the set of canonical backend events.
function expandStored(value: string[]): Set<string> {
  const set = new Set<string>();
  for (const v of value) {
    if (v === "updated") {
      set.add("release.found");
      set.add("download.submitted");
      set.add("download.completed");
    } else if (v === "error") {
      set.add("check.failed");
      set.add("session.expired");
    } else {
      set.add(v);
    }
  }
  return set;
}

export function EventPicker({ value, onChange }: EventPickerProps) {
  const t = useT();
  const selected = expandStored(value);

  // A group box is checked if ANY of its canonical events is selected.
  const groupChecked = (key: (typeof NOTIFIABLE_EVENTS)[number]) =>
    EVENT_GROUP_EVENTS[key].some((e) => selected.has(e));

  const toggleGroup = (key: (typeof NOTIFIABLE_EVENTS)[number], on: boolean) => {
    const next = expandStored(value);
    for (const e of EVENT_GROUP_EVENTS[key]) {
      if (on) next.add(e);
      else next.delete(e);
    }
    onChange([...next]);
  };

  return (
    <div className="flex flex-wrap items-center gap-4 text-sm">
      <span className="text-muted-foreground">{t("notifiers.notify_on")}:</span>
      {NOTIFIABLE_EVENTS.map((key) => (
        <label key={key} className="inline-flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={groupChecked(key)}
            onChange={(ev) => toggleGroup(key, ev.target.checked)}
          />
          {t(EVENT_LABELS[key])}
        </label>
      ))}
    </div>
  );
}
