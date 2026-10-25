import { useT } from "@/i18n";

interface TitleSyncToggleProps {
  checked: boolean;
  onChange: (next: boolean) => void;
}

/**
 * The per-topic "follow the tracker's title" toggle.
 *
 * While on, the scheduler keeps the topic's display name in sync with the
 * title the tracker reports on each check — the opt-in that undoes the
 * resolved-title lock (issue #90) for a topic that wants it. The form unticks
 * it the moment the user types a name, because typing a name is taking
 * ownership of it: the backend clears the flag on a rename, so showing it
 * still on would be a lie.
 */
export function TitleSyncToggle({ checked, onChange }: TitleSyncToggleProps) {
  const t = useT();
  return (
    <label className="flex items-start gap-2 pt-1 text-sm">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5"
      />
      <span>
        <span className="font-medium text-foreground">{t("topics.followTitle.label")}</span>
        <span className="block text-xs text-muted-foreground">{t("topics.followTitle.help")}</span>
      </span>
    </label>
  );
}
