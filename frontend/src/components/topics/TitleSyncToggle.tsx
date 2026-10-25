import { useT } from "@/i18n";

interface TitleSyncToggleProps {
  checked: boolean;
  /** Disable and explain while the save would discard the choice. */
  disabled?: boolean;
  onChange: (next: boolean) => void;
}

/**
 * The per-topic "follow the tracker's title" toggle.
 *
 * While on, the scheduler keeps the topic's display name in sync with the
 * title the tracker reports on each check — the opt-in that undoes the
 * resolved-title lock (issue #90) for a topic that wants it. The form forces
 * it off while the name is being edited, because typing a name is taking
 * ownership of it and the store clears the flag on a rename: showing it still
 * on would be a lie, and letting it be ticked would be a choice the save
 * silently discards.
 */
export function TitleSyncToggle({ checked, disabled, onChange }: TitleSyncToggleProps) {
  const t = useT();
  return (
    <div className="pt-1">
      <label className="flex items-start gap-2 text-sm">
        <input
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
          className="mt-0.5"
        />
        <span>
          <span className="font-medium text-foreground">{t("topics.followTitle.label")}</span>
          <span className="block text-xs text-muted-foreground">{t("topics.followTitle.help")}</span>
        </span>
      </label>
      {disabled && (
        <p className="mt-1 pl-6 text-xs text-muted-foreground">{t("topics.followTitle.lockedHint")}</p>
      )}
    </div>
  );
}
